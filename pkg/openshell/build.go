package openshell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// BuildRequest is an image to build with podman. OpenShell does not build images: a local
// gateway using the Podman driver runs images from local storage, and a remote gateway needs
// the image pushed to a registry it can pull from.
type BuildRequest struct {
	Tag           string
	Context       string
	Containerfile string
	BuildArgs     map[string]string
	Target        string
	Pull          bool
	NoCache       bool
	Push          bool
	Binary        string
}

// Image is a built image.
type Image struct {
	Ref string
	ID  string
}

// ImageRef qualifies a bare image name the way the Podman driver looks it up: "site:1" becomes
// "localhost/site:1"; a name with a registry or path ("quay.io/x/site") is left alone.
func ImageRef(tag string) string {
	name, _, _ := strings.Cut(tag, "@")
	if strings.Contains(name, "/") {
		return tag
	}
	return "localhost/" + tag
}

// Build runs podman build, writing its output to out, and pushes the image when asked.
func Build(ctx context.Context, req BuildRequest, out io.Writer) (*Image, error) {
	if req.Tag == "" {
		return nil, fmt.Errorf("a tag is required")
	}
	bin := req.Binary
	if bin == "" {
		bin = "podman"
	}
	ref := ImageRef(req.Tag)
	dir := req.Context
	if dir == "" {
		dir = "."
	}
	args := []string{"build", "-t", ref}
	if req.Containerfile != "" {
		args = append(args, "-f", req.Containerfile)
	}
	for _, kv := range pairs(req.BuildArgs) {
		args = append(args, "--build-arg", kv)
	}
	if req.Target != "" {
		args = append(args, "--target", req.Target)
	}
	if req.Pull {
		args = append(args, "--pull=always")
	}
	if req.NoCache {
		args = append(args, "--no-cache")
	}
	args = append(args, dir)
	if err := runStreaming(ctx, out, bin, args...); err != nil {
		return nil, err
	}
	var id bytes.Buffer
	inspect := exec.CommandContext(ctx, bin, "image", "inspect", "--format", "{{.Id}}", ref)
	inspect.Stdout = &id
	if err := inspect.Run(); err != nil {
		return nil, fmt.Errorf("%s image inspect %s: %w", bin, ref, err)
	}
	if req.Push {
		if err := runStreaming(ctx, out, bin, "push", ref); err != nil {
			return nil, err
		}
	}
	return &Image{Ref: ref, ID: strings.TrimSpace(id.String())}, nil
}

func runStreaming(ctx context.Context, out io.Writer, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	var tail tailBuffer
	w := io.MultiWriter(out, &tail)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w\n%s", bin, args[0], err, tail.String())
	}
	return nil
}

// tailBuffer keeps the last few KB written to it, for error messages.
type tailBuffer struct{ b []byte }

const tailMax = 4096

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > tailMax {
		t.b = t.b[len(t.b)-tailMax:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return strings.TrimSpace(string(t.b)) }
