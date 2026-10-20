package engine

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jctanner/markov/pkg/parser"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

func (e *Engine) loadArtifacts(artifacts map[string]parser.Artifact, runCtx map[string]any) (map[string]any, error) {
	result := make(map[string]any)

	for name, art := range artifacts {
		path, err := e.tmpl.Render(art.Path, runCtx)
		if err != nil {
			return nil, fmt.Errorf("artifact %q: rendering path: %w", name, err)
		}

		data, err := e.readArtifact(path, art.Source)
		if err != nil {
			if os.IsNotExist(err) && art.Optional {
				result[name] = nil
				continue
			}
			if art.Optional && strings.Contains(err.Error(), "not found") {
				result[name] = nil
				continue
			}
			return nil, fmt.Errorf("artifact %q: reading %s: %w", name, path, err)
		}

		switch art.Format {
		case "yaml":
			var parsed map[string]any
			if err := yaml.Unmarshal(data, &parsed); err != nil {
				return nil, fmt.Errorf("artifact %q: parsing YAML: %w", name, err)
			}
			result[name] = parsed

		case "markdown":
			frontmatter, content := parseMarkdown(string(data))
			result[name] = map[string]any{
				"frontmatter": frontmatter,
				"content":     content,
			}

		case "markdown_table":
			result[name] = parseMarkdownTable(string(data))

		default:
			result[name] = string(data)
		}
	}

	return result, nil
}

func (e *Engine) readArtifact(path string, source string) ([]byte, error) {
	switch source {
	case "local":
		return os.ReadFile(path)
	case "k8s":
		if e.k8s == nil || e.current().Namespace == "" {
			return nil, fmt.Errorf("k8s client not available for source: k8s")
		}
		return e.readFromK8s(path)
	default:
		if e.k8s != nil && e.current().Namespace != "" {
			return e.readFromK8s(path)
		}
		return os.ReadFile(path)
	}
}

func (e *Engine) readFromK8s(path string) ([]byte, error) {
	ns := e.current().Namespace
	ctx := context.Background()

	pods, err := e.k8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase=Running",
		Limit:         20,
	})
	if err != nil {
		return nil, fmt.Errorf("listing pods in %s: %w", ns, err)
	}

	for _, pod := range pods.Items {
		for _, c := range pod.Status.ContainerStatuses {
			if !c.Ready {
				continue
			}
			data, err := e.execCat(ns, pod.Name, c.Name, path)
			if err == nil {
				log.Printf("  read artifact from pod %s/%s", pod.Name, c.Name)
				return data, nil
			}
		}
	}

	return nil, fmt.Errorf("artifact not found at %s in any running pod in %s", path, ns)
}

func (e *Engine) execCat(namespace, pod, container, path string) ([]byte, error) {
	cfg, err := e.getRESTConfig()
	if err != nil {
		return nil, err
	}

	req := e.k8s.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   []string{"cat", path},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return nil, err
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return nil, fmt.Errorf("exec cat %s: %s: %w", path, stderr.String(), err)
	}

	return stdout.Bytes(), nil
}

func (e *Engine) getRESTConfig() (*rest.Config, error) {
	if e.restConfig != nil {
		return e.restConfig, nil
	}
	return nil, fmt.Errorf("REST config not set")
}

func parseMarkdown(raw string) (map[string]any, string) {
	scanner := bufio.NewScanner(strings.NewReader(raw))

	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return nil, raw
	}

	var fmLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		fmLines = append(fmLines, line)
	}

	var frontmatter map[string]any
	if err := yaml.Unmarshal([]byte(strings.Join(fmLines, "\n")), &frontmatter); err != nil {
		return nil, raw
	}

	var contentLines []string
	for scanner.Scan() {
		contentLines = append(contentLines, scanner.Text())
	}

	return frontmatter, strings.Join(contentLines, "\n")
}

func parseMarkdownTable(raw string) map[string]any {
	var headers []string
	var rows []map[string]any

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}

		cells := splitTableRow(line)

		if headers == nil {
			for i, h := range cells {
				cells[i] = toSnakeCase(h)
			}
			headers = cells
			continue
		}

		if len(cells) > 0 && strings.Trim(cells[0], "- ") == "" {
			continue
		}

		row := make(map[string]any)
		for i, h := range headers {
			if i < len(cells) {
				row[h] = cells[i]
			}
		}
		rows = append(rows, row)
	}

	result := map[string]any{
		"rows": rows,
	}
	for _, h := range headers {
		var vals []string
		for _, r := range rows {
			if v, ok := r[h].(string); ok {
				vals = append(vals, v)
			}
		}
		result[h] = strings.Join(vals, ",")
	}

	return result
}

func splitTableRow(line string) []string {
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

func toSnakeCase(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	return s
}
