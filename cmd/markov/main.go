package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/codify"
	"github.com/jctanner/markov/pkg/engine"
	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/openshell"
	"github.com/jctanner/markov/pkg/parser"
	"github.com/jctanner/markov/pkg/schema"
	"github.com/jctanner/markov/pkg/state"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	flagVars                []string
	flagWorkflow            string
	flagForks               int
	flagStateStore          string
	flagNamespace           string
	flagOpenShellTransport  string
	flagKubeconfig          string
	flagSteps               bool
	flagVerbose             bool
	flagCallbacks           []string
	flagCallbackHeaders     []string
	flagCallbackTLSInsecure bool
	flagCallbackTLSCert     string
	flagCallbackBufferSize  int
	flagDebug               bool
	flagRunID               string
	flagSourceIntegrity     string

	flagBreakpoints     []string
	flagBreakpointsFile string
	flagBreakShorthand  []string
	flagStep            bool
	flagAllowReload     bool
	flagControl         string

	flagRewind        []string
	flagRewindChanged bool

	saTokenPath     = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	saNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	stateStoreEnv   = "MARKOV_STATE_STORE"
)

func debugLog(format string, args ...any) {
	if flagDebug {
		log.Printf("[debug] "+format, args...)
	}
}

func defaultStateStorePath() string {
	if env := strings.TrimSpace(os.Getenv(stateStoreEnv)); env != "" {
		return env
	}
	if _, err := os.Stat(saTokenPath); err == nil {
		return "/tmp/markov-state.db"
	}
	return "./markov-state.db"
}

func addStateStoreFlag(cmd *cobra.Command, defaultValue string) {
	cmd.Flags().StringVar(&flagStateStore, "state-store", defaultValue, "SQLite path or Postgres DSN")
	if flag := cmd.Flags().Lookup("state-store"); flag != nil {
		flag.DefValue = state.RedactStoreLocation(defaultValue)
	}
}

// addCallbackFlags registers the event-callback flags (run and resume both report events).
func addCallbackFlags(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&flagCallbacks, "callback", nil, "Callback destination URL (repeatable). Schemes: jsonl://, http://, https://, grpc://, grpcs://")
	cmd.Flags().StringArrayVar(&flagCallbackHeaders, "callback-header", nil, "Extra HTTP headers for http callbacks (key=value, repeatable)")
	cmd.Flags().BoolVar(&flagCallbackTLSInsecure, "callback-tls-insecure", false, "Skip TLS verification for callback connections")
	cmd.Flags().StringVar(&flagCallbackTLSCert, "callback-tls-cert", "", "Client TLS certificate for callback connections")
	cmd.Flags().IntVar(&flagCallbackBufferSize, "callback-buffer-size", 1000, "Async send buffer size for callbacks")
}

// addDebugFlags registers the breakpoint and control flags (run and resume can both be debugged).
func addDebugFlags(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&flagBreakpoints, "breakpoint", nil, `Breakpoint as a JSON object, e.g. {"workflow":"main","step":"build"} (repeatable; needs --control stdin)`)
	cmd.Flags().StringVar(&flagBreakpointsFile, "breakpoints-file", "", "JSON file holding an array of breakpoint objects (needs --control stdin)")
	cmd.Flags().StringArrayVar(&flagBreakShorthand, "break", nil, "Breakpoint shorthand workflow.step, resolved against the real workflow and step names (repeatable; needs --control stdin)")
	cmd.Flags().BoolVar(&flagStep, "step", false, "Pause before every step (needs --control stdin)")
	cmd.Flags().StringVar(&flagControl, "control", "", "Read debugger commands as JSON lines from this source (only: stdin)")
}

func main() {
	stateStorePath := defaultStateStorePath()

	root := &cobra.Command{
		Use:   "markov",
		Short: "YAML workflow engine for Kubernetes",
	}

	runCmd := &cobra.Command{
		Use:   "run <file.yaml|directory>",
		Short: "Run a workflow",
		Args:  cobra.ExactArgs(1),
		RunE:  runWorkflow,
	}
	runCmd.Flags().StringArrayVar(&flagVars, "var", nil, "Override vars (key=value, repeatable)")
	runCmd.Flags().StringVar(&flagWorkflow, "workflow", "", "Run a specific workflow instead of entrypoint")
	runCmd.Flags().IntVar(&flagForks, "forks", 0, "Override global forks")
	addStateStoreFlag(runCmd, stateStorePath)
	runCmd.Flags().StringVar(&flagNamespace, "namespace", "", "Override K8s namespace")
	runCmd.Flags().StringVar(&flagOpenShellTransport, "openshell-transport", "", "Transport for openshell_* steps: cli or sdk (overrides the workflow file)")
	runCmd.Flags().StringVar(&flagKubeconfig, "kubeconfig", "", "K8s config path")
	runCmd.Flags().BoolVar(&flagVerbose, "verbose", false, "Show detailed execution output")
	runCmd.Flags().BoolVar(&flagDebug, "debug", false, "Show debug logging for flag parsing, callback setup, and K8s client init")
	runCmd.Flags().StringVar(&flagRunID, "run-id", "", "Use a specific run ID instead of generating one")
	runCmd.Flags().BoolVar(&flagAllowReload, "allow-reload", false, "Let reload_workflow steps replace the workflow definition while it runs (such a run cannot be resumed)")
	addCallbackFlags(runCmd)
	addDebugFlags(runCmd)

	resumeCmd := &cobra.Command{
		Use:   "resume <run_id>",
		Short: "Resume a failed or paused workflow run",
		Args:  cobra.ExactArgs(1),
		RunE:  resumeWorkflow,
	}
	addStateStoreFlag(resumeCmd, stateStorePath)
	resumeCmd.Flags().StringArrayVar(&flagVars, "var", nil, "Override vars before resuming (required for paused runs; key=value, repeatable)")
	// The same execution settings as run, so a resumed run behaves like the original.
	resumeCmd.Flags().StringVar(&flagNamespace, "namespace", "", "Override K8s namespace")
	resumeCmd.Flags().StringVar(&flagOpenShellTransport, "openshell-transport", "", "Transport for openshell_* steps: cli or sdk (overrides the workflow file)")
	resumeCmd.Flags().StringVar(&flagKubeconfig, "kubeconfig", "", "K8s config path")
	resumeCmd.Flags().BoolVar(&flagVerbose, "verbose", false, "Show detailed execution output")
	resumeCmd.Flags().BoolVar(&flagDebug, "debug", false, "Show debug logging for flag parsing, callback setup, and K8s client init")
	resumeCmd.Flags().IntVar(&flagForks, "forks", 0, "Override global forks")
	resumeCmd.Flags().StringVar(&flagSourceIntegrity, "source-integrity", "warn", "Source drift policy: warn, strict, or off")
	resumeCmd.Flags().StringArrayVar(&flagRewind, "rewind", nil, `Re-run from a step of the entrypoint workflow, as JSON, e.g. {"workflow":"main","step":"build"} (repeatable; the earliest wins)`)
	resumeCmd.Flags().BoolVar(&flagRewindChanged, "rewind-changed", false, "Re-run from the earliest completed step whose definition changed since it ran")
	resumeCmd.Flags().BoolVar(&flagAllowReload, "allow-reload", false, "Let reload_workflow steps replace the workflow definition while it runs (the resumed run then cannot be resumed again)")
	addCallbackFlags(resumeCmd)
	addDebugFlags(resumeCmd)

	var codifyCfg codify.Config
	codifyCmd := &cobra.Command{
		Use:   "codify <directory>",
		Short: "Codify a workflow: move work that needs no model out of its agent steps, one validated step at a time",
		Long: `Codify runs the workflow, lets a retro agent move one job out of its agent (claude) steps
into native steps in a copy of the project, runs the copy against a recording of what the
workflow fetched, and installs it when its outputs match. It repeats until no agent work that
can be codified is left. History (versions, metrics, recordings) goes to <directory>.codify.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			codifyCfg.Source = args[0]
			codifyCfg.Markov = bin
			codifyCfg.Out = os.Stdout
			cbs, err := buildCallbacks()
			if err != nil {
				return err
			}
			codifyCfg.Callbacks = cbs
			defer func() {
				for _, cb := range cbs {
					cb.Close()
				}
			}()
			// Stop (SIGTERM from the IDE, Ctrl-C) cancels the child runs and records the iteration.
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			_, err = codify.Run(ctx, codifyCfg)
			return err
		},
	}
	codifyCmd.Flags().StringVar(&codifyCfg.Workflow, "workflow", "", "The workflow to codify (it and the workflows it calls)")
	codifyCmd.Flags().StringVar(&codifyCfg.Model, "model", "claude-haiku-5-5", "Model for the retro agent and the judge")
	codifyCmd.Flags().IntVar(&codifyCfg.MaxIterations, "max-iterations", 8, "Stop after this many iterations")
	codifyCmd.Flags().Float64Var(&codifyCfg.BudgetUSD, "budget-usd", 5, "Stop once model spend reaches this (0: no limit)")
	codifyCmd.Flags().StringVar(&codifyCfg.CodifyDir, "codify-dir", "", "Where history goes (default <directory>.codify)")
	codifyCmd.Flags().StringArrayVar(&codifyCfg.Keep, "keep", nil, "A step (step or workflow.step) that must stay an agent step (repeatable)")
	codifyCmd.Flags().StringSliceVar(&codifyCfg.Fixed, "fixed", nil, "A workflow the target reaches that must not change, such as a reset or snapshot (repeatable or comma-separated)")
	codifyCmd.Flags().StringSliceVar(&codifyCfg.LiveHosts, "live-host", nil, "A host the runs reach for real instead of replaying a recording; only for services the workflow resets itself (repeatable or comma-separated)")
	codifyCmd.Flags().StringVar(&codifyCfg.PromptFile, "prompt-file", "", "Replace the retro agent's strategy prompt (a Go template; see the default in pkg/codify/retro.md)")
	_ = codifyCmd.MarkFlagRequired("workflow")
	addCallbackFlags(codifyCmd)

	statusCmd := &cobra.Command{
		Use:   "status <run_id>",
		Short: "Show run status",
		Args:  cobra.ExactArgs(1),
		RunE:  showStatus,
	}
	addStateStoreFlag(statusCmd, stateStorePath)
	statusCmd.Flags().BoolVar(&flagSteps, "steps", false, "Show individual step statuses")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all workflow runs",
		Args:  cobra.NoArgs,
		RunE:  listRuns,
	}
	addStateStoreFlag(listCmd, stateStorePath)

	validateCmd := &cobra.Command{
		Use:   "validate <file.yaml|directory>",
		Short: "Validate a workflow file or directory",
		Args:  cobra.ExactArgs(1),
		RunE:  validateWorkflow,
	}

	diagramCmd := &cobra.Command{
		Use:   "diagram <run_id>",
		Short: "Generate a Mermaid diagram of a completed run",
		Args:  cobra.ExactArgs(1),
		RunE:  showDiagram,
	}
	addStateStoreFlag(diagramCmd, stateStorePath)

	schemaCmd := &cobra.Command{
		Use:   "schema",
		Short: "Print the workflow format as JSON (step types, their parameters, and the file's fields)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(schema.Build())
		},
	}

	root.AddCommand(runCmd, resumeCmd, statusCmd, listCmd, validateCmd, diagramCmd, schemaCmd, codifyCmd)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func runWorkflow(cmd *cobra.Command, args []string) error {
	if flagDebug {
		flagVerbose = true
	}

	debugLog("flags: --run-id=%q --workflow=%q --namespace=%q --kubeconfig=%q --state-store=%q --forks=%d --verbose=%v",
		flagRunID, flagWorkflow, flagNamespace, flagKubeconfig, state.RedactStoreLocation(flagStateStore), flagForks, flagVerbose)
	debugLog("flags: --callback=%v --callback-header=%v --callback-tls-insecure=%v --callback-buffer-size=%d",
		flagCallbacks, flagCallbackHeaders, flagCallbackTLSInsecure, flagCallbackBufferSize)
	debugLog("flags: --var=%v", flagVars)

	wfFile, err := loadWorkflow(args[0])
	if err != nil {
		return err
	}

	vars := parseVarFlags(flagVars)

	debugLog("state store: %s", state.RedactStoreLocation(flagStateStore))
	store, err := state.OpenStore(flagStateStore)
	if err != nil {
		return err
	}
	defer store.Close()

	executors, err := buildExecutors(wfFile)
	if err != nil {
		return err
	}

	eng := engine.New(wfFile, store, executors)
	eng.Verbose = flagVerbose
	eng.RunID = flagRunID
	eng.SourcePath = args[0]
	eng.AllowReload = flagAllowReload
	eng.Loader = loadWorkflow
	configureSourceIdentity(eng)

	cbs, err := buildCallbacks()
	if err != nil {
		return err
	}
	eng.EventLogs = jsonlCallbackPaths()
	debugLog("callbacks: %d created from %d --callback flags", len(cbs), len(flagCallbacks))
	if len(cbs) > 0 {
		eng.SetCallbacks(cbs)
		defer eng.CloseCallbacks()
	}

	k8sClient, restCfg, err := getK8sClient()
	if err == nil {
		eng.SetK8sClient(k8sClient, restCfg)
	} else {
		debugLog("k8s client: unavailable: %v", err)
	}

	// Stop (SIGTERM, Ctrl-C) cancels the run: steps stop their own processes (a claude step's
	// whole process group) and the run is saved as failed, so it can be resumed.
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	dbg, err := buildDebugger(wfFile)
	if err != nil {
		return err
	}
	if dbg != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		dbg.SetCancel(cancel)
		eng.SetDebugger(dbg)
		go dbg.Serve(os.Stdin)
	}
	runID, err := eng.Run(ctx, flagWorkflow, vars)
	if err != nil {
		if engine.IsPaused(err) {
			fmt.Printf("run %s paused; inspect its gate receipt and resume with --var approval input\n", runID)
			return nil
		}
		fmt.Fprintf(os.Stderr, "run %s failed: %v\n", runID, err)
		return err
	}

	fmt.Printf("run %s completed successfully\n", runID)
	return nil
}

// loadWorkflow parses a workflow file or directory and applies the --forks and --namespace
// overrides. Runs use it for the initial definition and for every reload_workflow.
func loadWorkflow(path string) (*parser.WorkflowFile, error) {
	wfFile, err := parser.ParseFile(path)
	if err != nil {
		return nil, err
	}
	if flagForks > 0 {
		wfFile.Forks = flagForks
	}
	if flagNamespace != "" {
		wfFile.Namespace = flagNamespace
	}
	return wfFile, nil
}

// buildDebugger turns the breakpoint and control flags into a debugger, or nil when none are
// given. Every breakpoint is resolved against the workflow file before the run starts, so a
// typo fails here with the real names instead of silently never firing.
func buildDebugger(file *parser.WorkflowFile) (*engine.Debugger, error) {
	wantsBreaks := len(flagBreakpoints) > 0 || len(flagBreakShorthand) > 0 || flagBreakpointsFile != "" || flagStep
	switch flagControl {
	case "", "stdin":
	default:
		return nil, fmt.Errorf("--control %q is not supported (use stdin)", flagControl)
	}
	if !wantsBreaks && flagControl == "" {
		return nil, nil
	}
	if wantsBreaks && flagControl != "stdin" {
		return nil, fmt.Errorf("breakpoints and --step need --control stdin, so a paused run can be continued")
	}

	d := engine.NewDebugger(file)
	var bps []engine.Breakpoint
	for _, raw := range flagBreakpoints {
		bp, err := decodeBreakpoint([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("--breakpoint %s: %w", raw, err)
		}
		bps = append(bps, bp)
	}
	if flagBreakpointsFile != "" {
		data, err := os.ReadFile(flagBreakpointsFile)
		if err != nil {
			return nil, fmt.Errorf("--breakpoints-file: %w", err)
		}
		var raws []json.RawMessage
		if err := json.Unmarshal(data, &raws); err != nil {
			return nil, fmt.Errorf("--breakpoints-file %s: expected a JSON array of breakpoint objects: %w", flagBreakpointsFile, err)
		}
		for i, raw := range raws {
			bp, err := decodeBreakpoint(raw)
			if err != nil {
				return nil, fmt.Errorf("--breakpoints-file %s, entry %d: %w", flagBreakpointsFile, i, err)
			}
			bps = append(bps, bp)
		}
	}
	for _, s := range flagBreakShorthand {
		bp, err := d.ResolveShorthand(s)
		if err != nil {
			return nil, err
		}
		bps = append(bps, bp)
	}
	_, rejected := d.SetBreakpoints(bps)
	if len(rejected) > 0 {
		var msgs []string
		for _, r := range rejected {
			msgs = append(msgs, fmt.Sprint(r["error"]))
		}
		return nil, fmt.Errorf("unresolved breakpoint(s): %s", strings.Join(msgs, "; "))
	}
	d.SetStepMode(flagStep)
	return d, nil
}

// decodeBreakpoint reads one breakpoint object, rejecting unknown fields so a typo such as
// "iteraton" is an error rather than a breakpoint that quietly matches too much.
func decodeBreakpoint(raw []byte) (engine.Breakpoint, error) {
	var bp engine.Breakpoint
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&bp); err != nil {
		return bp, err
	}
	return bp, nil
}

func resumeWorkflow(cmd *cobra.Command, args []string) error {
	mode, err := engine.ParseSourceIntegrityMode(flagSourceIntegrity)
	if err != nil {
		return err
	}
	store, err := state.OpenStore(flagStateStore)
	if err != nil {
		return err
	}
	defer store.Close()

	// Stop (SIGTERM, Ctrl-C) cancels the run: steps stop their own processes (a claude step's
	// whole process group) and the run is saved as failed, so it can be resumed.
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	run, err := store.GetRun(ctx, args[0])
	if err != nil {
		return err
	}

	wfFile, err := loadWorkflow(run.WorkflowFile)
	if err != nil {
		return err
	}

	executors, err := buildExecutors(wfFile)
	if err != nil {
		return err
	}

	eng := engine.New(wfFile, store, executors)
	eng.Verbose = flagVerbose
	eng.SourcePath = run.WorkflowFile
	eng.AllowReload = flagAllowReload
	eng.Loader = loadWorkflow
	if k8sClient, restCfg, err := getK8sClient(); err == nil {
		eng.SetK8sClient(k8sClient, restCfg)
	} else {
		debugLog("k8s client: unavailable: %v", err)
	}
	eng.SourceIntegrityMode = mode
	configureSourceIdentity(eng)
	for _, raw := range flagRewind {
		var t engine.RewindTarget
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&t); err != nil {
			return fmt.Errorf("--rewind %s: %w", raw, err)
		}
		if t.Workflow == "" || t.Step == "" {
			return fmt.Errorf("--rewind %s: needs a workflow and a step", raw)
		}
		eng.Rewind = append(eng.Rewind, t)
	}
	eng.RewindChanged = flagRewindChanged

	cbs, err := buildCallbacks()
	if err != nil {
		return err
	}
	eng.EventLogs = jsonlCallbackPaths()
	if len(cbs) > 0 {
		eng.SetCallbacks(cbs)
		defer eng.CloseCallbacks()
	}
	dbg, err := buildDebugger(wfFile)
	if err != nil {
		return err
	}
	if dbg != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		dbg.SetCancel(cancel)
		eng.SetDebugger(dbg)
		go dbg.Serve(os.Stdin)
	}
	err = eng.ResumeWithVars(ctx, args[0], parseVarFlags(flagVars))
	if engine.IsPaused(err) {
		fmt.Printf("run %s remains paused; supply different --var approval input to resume\n", args[0])
		return nil
	}
	return err
}

func showStatus(cmd *cobra.Command, args []string) error {
	store, err := state.OpenStore(flagStateStore)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	run, err := store.GetRun(ctx, args[0])
	if err != nil {
		return err
	}

	fmt.Printf("Run:        %s\n", run.RunID)
	fmt.Printf("Workflow:   %s\n", run.Entrypoint)
	fmt.Printf("Status:     %s\n", run.Status)
	if run.SourceDigest != "" {
		fmt.Printf("Source:     %s\n", run.SourceDigest)
	} else {
		fmt.Println("Source:     unavailable (legacy run)")
	}
	fmt.Printf("Started:    %s\n", run.StartedAt.Format("2006-01-02 15:04:05"))
	if run.CompletedAt != nil {
		fmt.Printf("Completed:  %s\n", run.CompletedAt.Format("2006-01-02 15:04:05"))
	}
	checks, err := store.GetSourceChecks(ctx, args[0])
	if err != nil {
		return err
	}
	if len(checks) > 0 {
		last := checks[len(checks)-1]
		fmt.Printf("Source check: %s (%s)", last.Mode, last.CheckedAt.Format("2006-01-02 15:04:05"))
		if last.SourceDrifted {
			fmt.Printf(" — drift detected (%s)\n", last.ObservedDigest)
		} else {
			fmt.Println()
		}
	}

	if flagSteps {
		steps, err := store.GetSteps(ctx, args[0])
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Printf("%-30s %-12s %s\n", "STEP", "STATUS", "DURATION")
		fmt.Printf("%-30s %-12s %s\n", "----", "------", "--------")
		for _, s := range steps {
			dur := ""
			if s.StartedAt != nil && s.CompletedAt != nil {
				dur = s.CompletedAt.Sub(*s.StartedAt).Round(100 * 1e6).String()
			}
			fmt.Printf("%-30s %-12s %s\n", s.StepName, s.Status, dur)
			if s.Error != "" {
				fmt.Printf("  error: %s\n", s.Error)
			}
			if s.Status == state.StepPaused && s.OutputJSON != "" {
				fmt.Printf("  gate receipt: %s\n", s.OutputJSON)
			}
		}
	}

	return nil
}

func configureSourceIdentity(eng *engine.Engine) {
	if !state.IsPostgresDSN(flagStateStore) {
		eng.SourceExcludes = []string{flagStateStore}
	}
}

func listRuns(cmd *cobra.Command, args []string) error {
	store, err := state.OpenStore(flagStateStore)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	runs, err := store.ListRuns(ctx)
	if err != nil {
		return err
	}

	if len(runs) == 0 {
		fmt.Println("No runs found.")
		return nil
	}

	fmt.Printf("%-10s %-25s %-12s %-20s %s\n", "RUN ID", "WORKFLOW", "STATUS", "STARTED", "DURATION")
	fmt.Printf("%-10s %-25s %-12s %-20s %s\n", "------", "--------", "------", "-------", "--------")
	for _, r := range runs {
		dur := ""
		if r.CompletedAt != nil {
			dur = r.CompletedAt.Sub(r.StartedAt).Round(100 * 1e6).String()
		}
		fmt.Printf("%-10s %-25s %-12s %-20s %s\n",
			r.RunID, r.Entrypoint, r.Status,
			r.StartedAt.Format("2006-01-02 15:04:05"), dur)
	}

	return nil
}

func showDiagram(cmd *cobra.Command, args []string) error {
	store, err := state.OpenStore(flagStateStore)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	tree, err := buildRunTree(ctx, store, args[0])
	if err != nil {
		return err
	}

	fmt.Print(generateMermaid(tree))
	return nil
}

func validateWorkflow(cmd *cobra.Command, args []string) error {
	_, err := parser.ParseFile(args[0])
	if err != nil {
		return err
	}
	fmt.Println("valid")
	return nil
}

func buildCallbacks() ([]callback.Callback, error) {
	if len(flagCallbacks) == 0 {
		return nil, nil
	}

	headers := make(map[string]string)
	for _, h := range flagCallbackHeaders {
		parts := strings.SplitN(h, "=", 2)
		if len(parts) == 2 {
			headers[parts[0]] = parts[1]
		}
	}

	var cbs []callback.Callback
	for _, u := range flagCallbacks {
		debugLog("callback: parsing %q", u)
		cb, err := callback.ParseCallbackURL(u, headers, flagCallbackBufferSize, flagCallbackTLSInsecure, flagCallbackTLSCert)
		if err != nil {
			return nil, fmt.Errorf("parsing callback %q: %w", u, err)
		}
		debugLog("callback: created %T for %s", cb, u)
		cbs = append(cbs, cb)
	}
	return cbs, nil
}

// jsonlCallbackPaths lists the absolute paths of the jsonl:// callbacks, for markov_event_logs.
func jsonlCallbackPaths() []string {
	var paths []string
	for _, u := range flagCallbacks {
		if p, ok := strings.CutPrefix(u, "jsonl://"); ok && p != "" {
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
			paths = append(paths, p)
		}
	}
	return paths
}

func parseVarFlags(vars []string) map[string]any {
	result := make(map[string]any)
	for _, v := range vars {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = engine.CoerceString(parts[1])
		}
	}
	return result
}

func resolveNamespace(wfNamespace, flagNS string) string {
	ns := wfNamespace
	if ns != "" {
		debugLog("namespace: using workflow namespace %q", ns)
		return ns
	}
	ns = flagNS
	if ns != "" {
		debugLog("namespace: using --namespace flag %q", ns)
		return ns
	}
	if data, err := os.ReadFile(saNamespacePath); err == nil {
		ns = strings.TrimSpace(string(data))
		debugLog("namespace: using service account namespace %q", ns)
		return ns
	}
	debugLog("namespace: using default")
	return "default"
}

// openShellConfig is the file's openshell settings, with --openshell-transport applied.
func openShellConfig(wf *parser.WorkflowFile) openshell.Config {
	var cfg openshell.Config
	if o := wf.OpenShell; o != nil {
		cfg = openshell.Config{Transport: o.Transport, Workspace: o.Workspace, Gateway: o.Gateway, Binary: o.Binary, TokenEnv: o.TokenEnv, CAFile: o.CAFile, CertFile: o.CertFile, KeyFile: o.KeyFile}
	}
	if flagOpenShellTransport != "" {
		cfg.Transport = flagOpenShellTransport
	}
	return cfg
}

func buildExecutors(wf *parser.WorkflowFile) (map[string]executor.Executor, error) {
	executors := map[string]executor.Executor{
		"shell_exec":       executor.NewShellExec(),
		"script_exec":      executor.NewScriptExec(),
		"write_file":       executor.NewWriteFile(),
		"claude":           executor.NewClaude().WithOpenShell(openShellConfig(wf)),
		"ansible":          executor.NewAnsible(),
		"ansible_playbook": executor.NewAnsiblePlaybook(),
		"prompt":           executor.NewPrompt(),
		"http_request":     executor.NewHTTPRequest(),
		"jev":              executor.NewJev(wf.Connections),
	}
	for name, ex := range executor.NewOpenShellSteps(openShellConfig(wf)) {
		executors[name] = ex
	}

	namespace := resolveNamespace(wf.Namespace, flagNamespace)

	k8sClient, _, err := getK8sClient()
	if err != nil {
		log.Printf("warning: k8s client unavailable: %v (k8s_job and k8s_job_wait steps will fail)", err)
	} else {
		executors["k8s_job"] = executor.NewK8sJob(k8sClient, namespace)
		executors["k8s_job_wait"] = executor.NewK8sJobWait(k8sClient, namespace)
	}

	debugLog("executors: registered %v", func() []string {
		names := make([]string, 0, len(executors))
		for k := range executors {
			names = append(names, k)
		}
		return names
	}())

	return executors, nil
}

func getK8sClient() (kubernetes.Interface, *rest.Config, error) {
	if restConfig, err := rest.InClusterConfig(); err == nil {
		debugLog("k8s client: using in-cluster config (host=%s)", restConfig.Host)
		client, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return nil, nil, err
		}
		return client, restConfig, nil
	}
	debugLog("k8s client: in-cluster config not available, trying kubeconfig")

	kubeconfig := flagKubeconfig
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	var config *clientcmd.ClientConfig
	debugLog("k8s client: kubeconfig=%q", kubeconfig)
	if kubeconfig != "" {
		c := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
			&clientcmd.ConfigOverrides{},
		)
		config = &c
	} else {
		c := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{},
		)
		config = &c
	}

	restConfig, err := (*config).ClientConfig()
	if err != nil {
		return nil, nil, err
	}

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, err
	}
	return client, restConfig, nil
}
