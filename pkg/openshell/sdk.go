package openshell

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	dm "github.com/NVIDIA/OpenShell/sdk/go/proto/datamodelv1"
	pb "github.com/NVIDIA/OpenShell/sdk/go/proto/openshellv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// SDK is the transport that talks gRPC to the gateway through the OpenShell Go SDK. Create and
// delete use the SDK's typed client. Exec uses the interactive exec RPC directly, because the
// SDK's own interactive exec always asks for a TTY and its other calls cannot send stdin or a
// timeout; uploads and downloads stream a tar archive through that exec, since the SDK's file
// client has no transport yet.
type SDK struct {
	workspace string
	client    *v1.Client
	conn      *grpc.ClientConn
	raw       pb.OpenShellClient
}

// stdinChunk stays well under the gateway's 1 MiB gRPC message limit.
const stdinChunk = 256 << 10

// NewSDK connects to the gateway cfg resolves to (see ResolveEndpoint).
func NewSDK(cfg Config) (*SDK, error) {
	ep, err := ResolveEndpoint(cfg)
	if err != nil {
		return nil, err
	}
	auth := v1.NoAuth()
	if ep.Token != "" {
		auth = v1.StaticToken(ep.Token)
	}
	var tlsCfg *v1.TLSConfig
	if !strings.HasPrefix(ep.Address, "http://") {
		tlsCfg = &v1.TLSConfig{CAFile: ep.CAFile, CertFile: ep.CertFile, KeyFile: ep.KeyFile}
	}
	client, err := v1.NewClient(v1.Config{Address: ep.Address, Auth: auth, TLS: tlsCfg})
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", ep.Address, err)
	}
	conn, err := rawConn(ep, auth)
	if err != nil {
		client.Close()
		return nil, err
	}
	ws := cfg.Workspace
	if ws == "" {
		ws = "default"
	}
	return &SDK{workspace: ws, client: client, conn: conn, raw: pb.NewOpenShellClient(conn)}, nil
}

// rawConn dials the gateway the way the SDK does, for the RPCs it does not wrap.
func rawConn(ep *Endpoint, auth credentials.PerRPCCredentials) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption
	addr := ep.Address
	if strings.HasPrefix(addr, "http://") {
		addr = strings.TrimPrefix(addr, "http://")
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		tc, err := tlsConfig(ep)
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tc)), grpc.WithPerRPCCredentials(auth))
	}
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", addr, err)
	}
	return conn, nil
}

// Close closes the gateway connections.
func (s *SDK) Close() error {
	return errors.Join(s.client.Close(), s.conn.Close())
}

func (s *SDK) Create(ctx context.Context, req CreateRequest) (*Sandbox, error) {
	if req.Name == "" {
		return nil, errors.New("a sandbox name is required")
	}
	if req.Policy != "" {
		return nil, errors.New("policy files need the cli transport: the sdk cannot parse OpenShell policy YAML")
	}
	spec := &v1.SandboxSpec{Environment: req.Env, Providers: req.Providers}
	limits := map[string]any{}
	if req.CPU != "" {
		limits["cpu"] = req.CPU
	}
	if req.Memory != "" {
		limits["memory"] = req.Memory
	}
	if req.Image != "" || len(limits) > 0 {
		spec.Template = &v1.SandboxTemplate{Image: req.Image}
		if len(limits) > 0 {
			spec.Template.Resources = map[string]any{"limits": limits}
		}
	}
	switch req.GPU {
	case "":
	case "any":
		spec.GPU = true
	default:
		var n uint32
		if _, err := fmt.Sscan(req.GPU, &n); err != nil {
			return nil, fmt.Errorf("gpu must be true or a count, not %q", req.GPU)
		}
		spec.GPU, spec.GPUCount = true, &n
	}
	var opts []v1.CreateOption
	if len(req.Labels) > 0 {
		opts = append(opts, v1.WithLabels(req.Labels))
	}
	if _, err := s.client.Sandboxes().Create(ctx, s.workspace, req.Name, spec, opts...); err != nil {
		return nil, fmt.Errorf("create %s: %w", req.Name, err)
	}
	sb, err := s.client.Sandboxes().WaitReady(ctx, s.workspace, req.Name, v1.WaitOptions{PollInterval: 250 * time.Millisecond})
	if err != nil {
		// Do not leave a sandbox that never became ready behind.
		delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = s.client.Sandboxes().Delete(delCtx, s.workspace, req.Name)
		return nil, fmt.Errorf("waiting for %s: %w", req.Name, err)
	}
	return &Sandbox{Name: sb.Name, ID: sb.ID, Phase: string(sb.Status.Phase), Workspace: sb.Workspace}, nil
}

func (s *SDK) Delete(ctx context.Context, names []string) error {
	var errs []error
	for _, n := range names {
		if _, err := s.client.Sandboxes().Delete(ctx, s.workspace, n); err != nil && !v1.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("delete %s: %w", n, err))
		}
	}
	return errors.Join(errs...)
}

func (s *SDK) Exec(ctx context.Context, req ExecRequest, stdout, stderr io.Writer) (int, error) {
	start := &pb.ExecSandboxRequest{
		Sandbox:        req.Sandbox,
		Command:        req.Command,
		Workdir:        req.Workdir,
		Environment:    req.Env,
		WorkspaceScope: &dm.WorkspaceSelector{Selection: &dm.WorkspaceSelector_Workspace{Workspace: s.workspace}},
	}
	streamCtx, cancel, timedOut := execDeadline(ctx, req)
	defer cancel()
	stream, err := s.raw.ExecSandboxInteractive(streamCtx)
	if err != nil {
		return -1, fmt.Errorf("exec: %w", err)
	}
	if err := stream.Send(&pb.ExecSandboxInput{Payload: &pb.ExecSandboxInput_Start{Start: start}}); err != nil {
		return -1, fmt.Errorf("exec: %w", err)
	}

	sendErr := make(chan error, 1)
	go func() { sendErr <- sendStdin(stream, req.Stdin) }()

	code, recvErr := -1, error(nil)
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			recvErr = err
			break
		}
		switch p := ev.Payload.(type) {
		case *pb.ExecSandboxEvent_Stdout:
			if _, err := stdout.Write(p.Stdout.GetData()); err != nil {
				recvErr = err
			}
		case *pb.ExecSandboxEvent_Stderr:
			if _, err := stderr.Write(p.Stderr.GetData()); err != nil {
				recvErr = err
			}
		case *pb.ExecSandboxEvent_Exit:
			code = int(p.Exit.GetExitCode())
		}
		if recvErr != nil {
			break
		}
	}
	cancel()
	if err := <-sendErr; err != nil && recvErr == nil && code < 0 {
		recvErr = err
	}
	if timedOut() {
		return TimeoutExitCode, nil
	}
	if recvErr != nil {
		return -1, fmt.Errorf("exec in %s: %w", req.Sandbox, recvErr)
	}
	if code < 0 {
		return -1, fmt.Errorf("exec in %s: the stream ended without an exit status", req.Sandbox)
	}
	return code, nil
}

// sendStdin streams r in chunks, then closes the send side so the command sees end of input.
func sendStdin(stream grpc.BidiStreamingClient[pb.ExecSandboxInput, pb.ExecSandboxEvent], r io.Reader) error {
	if r != nil {
		buf := make([]byte, stdinChunk)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				if serr := stream.Send(&pb.ExecSandboxInput{Payload: &pb.ExecSandboxInput_Stdin{Stdin: chunk}}); serr != nil {
					return serr
				}
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
		}
	}
	return stream.CloseSend()
}

func (s *SDK) Upload(ctx context.Context, sandbox, src, destDir string, gitIgnore bool) error {
	if _, err := os.Lstat(src); err != nil {
		return err
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(writeTar(pw, src, gitIgnore)) }()
	defer pr.Close()
	var errOut strings.Builder
	code, err := s.Exec(ctx, ExecRequest{
		Sandbox: sandbox,
		Command: []string{"sh", "-c", `mkdir -p "$1" && tar -xf - --no-same-owner -C "$1"`, "sh", destDir},
		Stdin:   pr,
	}, io.Discard, &errOut)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("upload to %s:%s: tar exited %d: %s", sandbox, destDir, code, strings.TrimSpace(errOut.String()))
	}
	return nil
}

func (s *SDK) Download(ctx context.Context, sandbox, src, destDir string) error {
	pr, pw := io.Pipe()
	extracted := make(chan error, 1)
	go func() {
		err := readTar(pr, destDir)
		// Drain what is left so the exec does not block on a full pipe.
		_, _ = io.Copy(io.Discard, pr)
		extracted <- err
	}()
	var errOut strings.Builder
	code, err := s.Exec(ctx, ExecRequest{
		Sandbox: sandbox,
		Command: []string{"sh", "-c", `cd "$(dirname "$1")" && tar -cf - "$(basename "$1")"`, "sh", strings.TrimRight(src, "/")},
	}, pw, &errOut)
	pw.Close()
	tarErr := <-extracted
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("download %s:%s: tar exited %d: %s", sandbox, src, code, strings.TrimSpace(errOut.String()))
	}
	return tarErr
}

// tlsConfig mirrors the SDK's TLS settings for the raw connection.
func tlsConfig(ep *Endpoint) (*tls.Config, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if ep.CAFile != "" {
		pem, err := os.ReadFile(ep.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no CA certificate", ep.CAFile)
		}
		tc.RootCAs = pool
	}
	if ep.CertFile != "" || ep.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(ep.CertFile, ep.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	return tc, nil
}

// EnsureProfile is not available on the sdk transport: the SDK takes typed profiles, and only
// the openshell CLI parses profile YAML.
func (s *SDK) EnsureProfile(ctx context.Context, path string) (ProfileChange, error) {
	return "", errors.New("profile files need the cli transport: the sdk cannot parse OpenShell profile YAML")
}

func (s *SDK) EnsureProvider(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	if req.Name == "" || req.Type == "" {
		return nil, errors.New("a provider needs a name and a type")
	}
	keys := sortedKeys(req.Credentials)
	existing, err := s.client.Providers().Get(ctx, s.workspace, req.Name)
	if v1.IsNotFound(err) {
		_, err := s.client.Providers().Create(ctx, s.workspace, &v1.Provider{
			// Profiles imported without --global live in the workspace; say which one.
			Name: req.Name, Type: req.Type, Spec: v1.ProviderSpec{Credentials: req.Credentials, ProfileWorkspace: s.workspace},
		})
		if err != nil {
			return nil, fmt.Errorf("create provider %s: %w", req.Name, err)
		}
		return &ProviderResult{Created: true, CredentialKeys: keys}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", req.Name, err)
	}
	if existing.Type != req.Type {
		return nil, fmt.Errorf("provider %s exists with type %s, not %s; delete it to change its type", req.Name, existing.Type, req.Type)
	}
	if len(keys) > 0 {
		updated := *existing
		updated.Spec.Credentials = req.Credentials
		if _, err := s.client.Providers().Update(ctx, s.workspace, &updated); err != nil {
			return nil, fmt.Errorf("update provider %s: %w", req.Name, err)
		}
	}
	return &ProviderResult{CredentialKeys: keys}, nil
}
