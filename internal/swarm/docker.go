package swarm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// DockerCLI runs previews through the docker command.
type DockerCLI struct{}

func (DockerCLI) run(ctx context.Context, out io.Writer, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if out != nil {
		cmd.Stdout, cmd.Stderr = io.MultiWriter(&stdout, out), io.MultiWriter(&stderr, out)
	}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("docker %s: %s", args[0], msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (d DockerCLI) Available(ctx context.Context) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return err
	}
	_, err := d.run(ctx, nil, "version", "--format", "{{.Server.Version}}")
	return err
}

func labelArgs(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var args []string
	for _, k := range keys {
		args = append(args, "--label", k+"="+labels[k])
	}
	return args
}

func (d DockerCLI) Build(ctx context.Context, contextDir, dockerfile, tag string, labels map[string]string, out io.Writer) error {
	args := append([]string{"build", "--file", dockerfile, "--tag", tag}, labelArgs(labels)...)
	_, err := d.run(ctx, out, append(args, contextDir)...)
	return err
}

// RunArgs is the docker run command line for a spec. Every budget is a flag:
// CPU, memory (swap included, so memory cannot spill), process count, and a
// single published port bound to loopback only.
func RunArgs(spec RunSpec) []string {
	mem := strconv.FormatInt(spec.Budget.MemoryBytes, 10)
	args := []string{"run", "--detach", "--init",
		"--name", spec.Name,
		"--cpus", strconv.FormatFloat(spec.Budget.CPUs, 'f', -1, 64),
		"--memory", mem, "--memory-swap", mem,
		"--pids-limit", strconv.Itoa(spec.Budget.Pids),
		"--security-opt", "no-new-privileges",
		"--publish", fmt.Sprintf("127.0.0.1::%d", spec.Port),
	}
	args = append(args, labelArgs(spec.Labels)...)
	if spec.Mount != "" {
		args = append(args, "--mount", "type=bind,source="+spec.Mount+",target=/workspace,readonly", "--workdir", "/workspace")
	}
	// A configured command is the whole argv: it replaces the image's
	// entrypoint as well as its CMD, so the config means what it says.
	if len(spec.Command) > 0 {
		args = append(args, "--entrypoint", spec.Command[0])
	}
	args = append(args, "--", spec.Image)
	if len(spec.Command) > 1 {
		args = append(args, spec.Command[1:]...)
	}
	return args
}

func (d DockerCLI) Run(ctx context.Context, spec RunSpec) error {
	_, err := d.run(ctx, nil, RunArgs(spec)...)
	return err
}

func (d DockerCLI) HostPort(ctx context.Context, name string, port int) (int, error) {
	out, err := d.run(ctx, nil, "port", name, fmt.Sprintf("%d/tcp", port))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		i := strings.LastIndex(line, ":")
		if i < 0 || !strings.HasPrefix(line, "127.0.0.1:") {
			continue
		}
		if p, err := strconv.Atoi(line[i+1:]); err == nil {
			return p, nil
		}
	}
	return 0, fmt.Errorf("docker published no loopback port for %s %d/tcp: %q", name, port, out)
}

func (d DockerCLI) List(ctx context.Context, label, value string) ([]Container, error) {
	format := strings.Join([]string{"{{.Names}}", "{{.State}}",
		`{{.Label "` + LabelCandidate + `"}}`, `{{.Label "` + LabelPreview + `"}}`,
		`{{.Label "` + LabelGeneration + `"}}`, `{{.Label "` + LabelDeadline + `"}}`}, "\t")
	out, err := d.run(ctx, nil, "ps", "--all", "--no-trunc", "--filter", "label="+label+"="+value, "--format", format)
	if err != nil {
		return nil, err
	}
	var list []Container
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 6 || f[0] == "" {
			continue
		}
		list = append(list, Container{Name: f[0], Running: f[1] == "running", Candidate: f[2], Preview: f[3], Generation: f[4], Deadline: f[5]})
	}
	return list, nil
}

func (d DockerCLI) Logs(ctx context.Context, name string, tail int) string {
	cmd := exec.CommandContext(ctx, "docker", "logs", "--tail", strconv.Itoa(tail), name)
	out, _ := cmd.CombinedOutput()
	return strings.TrimSpace(string(out))
}

func (d DockerCLI) Remove(ctx context.Context, names ...string) error {
	if len(names) == 0 {
		return nil
	}
	_, err := d.run(ctx, nil, append([]string{"rm", "--force", "--volumes"}, names...)...)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "no such container") {
		return nil
	}
	return err
}

func (d DockerCLI) RemoveImage(ctx context.Context, tag string) error {
	_, err := d.run(ctx, nil, "image", "rm", "--force", tag)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "no such image") {
		return nil
	}
	return err
}
