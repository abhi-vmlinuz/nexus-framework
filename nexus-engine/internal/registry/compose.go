package registry

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nexus-oss/nexus/nexus-engine/internal/state"
	"gopkg.in/yaml.v3"
)

// composeFile represents a minimal docker-compose.yml structure.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Build struct {
		Context    string `yaml:"context"`
		Dockerfile string `yaml:"dockerfile"`
	} `yaml:"build"`
	Image       string            `yaml:"image"`
	Ports       []string          `yaml:"ports"`
	Expose      []string          `yaml:"expose"`
	Environment yaml.Node         `yaml:"environment"`
	Deploy      *composeDeploy    `yaml:"deploy,omitempty"`
	HealthCheck *composeHealth    `yaml:"healthcheck,omitempty"`
}

type composeDeploy struct {
	Resources struct {
		Limits struct {
			CPUs   string `yaml:"cpus,omitempty"`
			Memory string `yaml:"memory,omitempty"`
		} `yaml:"limits,omitempty"`
	} `yaml:"resources,omitempty"`
}

type composeHealth struct {
	Test        yaml.Node `yaml:"test"`
	Interval    string    `yaml:"interval,omitempty"`
	Timeout     string    `yaml:"timeout,omitempty"`
	Retries     int       `yaml:"retries,omitempty"`
	StartPeriod string    `yaml:"start_period,omitempty"`
}

// ParseComposeResult is returned after a compose parse + build operation.
type ParseComposeResult struct {
	Containers []state.ContainerSpec
	AllPorts   []int
	Build      ComposeBuildResult
}

// ComposeBuildResult captures the overall build outcome for a compose challenge.
type ComposeBuildResult struct {
	Status     string                 `json:"status"` // success | partial_failure | full_failure
	StartedAt  time.Time              `json:"started_at"`
	CompletedAt time.Time             `json:"completed_at"`
	DurationMs int64                  `json:"duration_ms"`
	Containers []ContainerBuildResult `json:"containers"`
	Tooling    ToolingInfo            `json:"tooling"`
}

// ParseAndBuild reads a docker-compose.yml, builds any local service images
// via nerdctl (engine runs as root), pulls public images, and returns the
// resulting container specs ready for pod registration.
//
// On partial failure (some services build, others fail), it returns an error
// but also populates result.Build with per-container status so the caller
// can include detailed failure info in the API response.
func (b *Builder) ParseAndBuild(challengeName, composePath string) (*ParseComposeResult, error) {
	start := time.Now()
	tooling := GetToolingVersions()

	// Validate the path is within allowed directories (path traversal protection).
	if err := ValidateBuildPath(composePath, b.allowedPaths); err != nil {
		return nil, fmt.Errorf("path validation: %w", err)
	}

	data, err := os.ReadFile(composePath)
	if err != nil {
		return nil, fmt.Errorf("cannot read compose file: %w", err)
	}

	// Expand env vars in the compose file using an allow-list to prevent
	// leaking host environment variables into compose files.
	expanded := safeExpandEnv(string(data), map[string]string{
		"REGISTRY_URL": b.cfg.URL,
	})

	var cf composeFile
	if err := yaml.Unmarshal([]byte(expanded), &cf); err != nil {
		return nil, fmt.Errorf("invalid compose yaml: %w", err)
	}
	if len(cf.Services) == 0 {
		return nil, fmt.Errorf("no services defined in compose file %s", composePath)
	}

	composeDir := filepath.Dir(composePath)
	result := &ParseComposeResult{
		Build: ComposeBuildResult{
			StartedAt: start,
			Tooling:   tooling,
		},
	}
	portSeen := map[int]bool{}

	var failedServices []string
	var lastError error

	for svcName, svc := range cf.Services {
		var imageRef string
		var buildResult ContainerBuildResult

		if svc.Build.Context != "" {
			// Local service: build with nerdctl.
			context := svc.Build.Context
			if !filepath.IsAbs(context) {
				context = filepath.Join(composeDir, context)
			}
			dockerfile := svc.Build.Dockerfile
			if dockerfile != "" && !filepath.IsAbs(dockerfile) {
				dockerfile = filepath.Join(context, dockerfile)
			}
			imageRef = fmt.Sprintf("%s/%s-%s:latest", b.cfg.URL, sanitizeImageName(challengeName), sanitizeImageName(svcName))

			buildResult = b.BuildFromSource(svcName, imageRef, context, dockerfile)
		} else if svc.Image != "" {
			// Public image: pull into k8s.io namespace.
			imageRef = svc.Image
			buildResult = b.PullImage(svcName, imageRef)
		} else {
			buildResult = ContainerBuildResult{
				Name:   svcName,
				Status: "failed",
				Error:  fmt.Sprintf("service %q: must have either build or image", svcName),
			}
		}

		// Set ports on the build result.
		if ports, err := parseComposePorts(svc.Ports); err == nil {
			buildResult.Ports = ports
		}

		result.Build.Containers = append(result.Build.Containers, buildResult)

		if buildResult.Status == "failed" {
			failedServices = append(failedServices, svcName)
			lastError = fmt.Errorf("service %q: %s", svcName, buildResult.Error)
			continue
		}

		// Parse ports from both "ports" and "expose" keys.
		ports, err := parseComposePorts(svc.Ports)
		if err != nil {
			failedServices = append(failedServices, svcName)
			lastError = fmt.Errorf("service %q: %w", svcName, err)
			result.Build.Containers[len(result.Build.Containers)-1].Status = "failed"
			result.Build.Containers[len(result.Build.Containers)-1].Error = err.Error()
			continue
		}
		for _, p := range ports {
			if !portSeen[p] {
				portSeen[p] = true
				result.AllPorts = append(result.AllPorts, p)
			}
		}

		env := parseComposeEnv(svc.Environment)

		spec := state.ContainerSpec{
			Name:  svcName,
			Image: imageRef,
			Ports: ports,
			Env:   env,
		}

		// Convert Resources
		if (svc.Deploy != nil && svc.Deploy.Resources.Limits.CPUs != "") || (svc.Deploy != nil && svc.Deploy.Resources.Limits.Memory != "") {
			spec.Resources = &state.Resources{
				CPU:    svc.Deploy.Resources.Limits.CPUs,
				Memory: svc.Deploy.Resources.Limits.Memory,
			}
		}

		// Convert HealthCheck to ReadinessProbe
		if svc.HealthCheck != nil {
			spec.ReadinessProbe = parseHealthCheck(svc.HealthCheck)
		}

		result.Containers = append(result.Containers, spec)
	}

	// Finalize build metadata.
	completedAt := time.Now()
	result.Build.CompletedAt = completedAt
	result.Build.DurationMs = completedAt.Sub(start).Milliseconds()

	if len(failedServices) > 0 {
		if len(failedServices) == len(cf.Services) {
			result.Build.Status = "full_failure"
		} else {
			result.Build.Status = "partial_failure"
		}
		return result, fmt.Errorf("%d of %d services failed to build: %w", len(failedServices), len(cf.Services), lastError)
	}

	result.Build.Status = "success"
	return result, nil
}

// buildAndPush runs nerdctl build + push. Engine runs as root via systemd.
func (b *Builder) buildAndPush(imageRef, context, dockerfile string) error {
	args := []string{
		"build",
		"--namespace", "k8s.io",
		"-t", imageRef,
	}
	if dockerfile != "" {
		args = append(args, "-f", dockerfile)
	}
	args = append(args, context)

	var out bytes.Buffer
	cmd := exec.Command("nerdctl", args...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nerdctl build failed: %w\noutput: %s", err, out.String())
	}

	out.Reset()
	pushArgs := []string{"push", "--namespace", "k8s.io"}
	if auth := b.authArgs(); len(auth) > 0 {
		pushArgs = append(pushArgs, auth...)
	}
	pushArgs = append(pushArgs, imageRef)

	pushCmd := exec.Command("nerdctl", pushArgs...)
	pushCmd.Stdout = &out
	pushCmd.Stderr = &out
	if err := pushCmd.Run(); err != nil {
		return fmt.Errorf("nerdctl push failed: %w\noutput: %s", err, out.String())
	}
	return nil
}

// pull pre-pulls a public image into the k8s.io namespace.
func (b *Builder) pull(imageRef string) error {
	var out bytes.Buffer
	cmd := exec.Command("nerdctl", "pull", "--namespace", "k8s.io", imageRef)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nerdctl pull %q failed: %w\noutput: %s", imageRef, err, out.String())
	}
	return nil
}

// parseComposePorts parses port mappings like "8080:80" or "443" into ints.
// We always extract the container-side (right) port.
func parseComposePorts(raw []string) ([]int, error) {
	var ports []int
	for _, r := range raw {
		parts := strings.Split(r, ":")
		pStr := strings.Split(parts[len(parts)-1], "/")[0] // strip /tcp etc.
		var p int
		if _, err := fmt.Sscanf(pStr, "%d", &p); err != nil {
			return nil, fmt.Errorf("invalid port %q in compose file", r)
		}
		ports = append(ports, p)
	}
	return ports, nil
}

// parseComposeEnv extracts environment variables from a compose service.
// Supports both map format (KEY: value) and list format (KEY=value).
func parseComposeEnv(node yaml.Node) map[string]string {
	env := make(map[string]string)
	if node.Kind == 0 {
		return env
	}

	// Map format: environment: {KEY: value}
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content)-1; i += 2 {
			key := node.Content[i].Value
			val := node.Content[i+1].Value
			env[key] = val
		}
		return env
	}

	// List format: environment: [KEY=value]
	if node.Kind == yaml.SequenceNode {
		for _, item := range node.Content {
			parts := strings.SplitN(item.Value, "=", 2)
			if len(parts) == 2 {
				env[parts[0]] = parts[1]
			}
		}
		return env
	}

	return env
}

func parseHealthCheck(hc *composeHealth) *state.ReadinessProbe {
	probe := &state.ReadinessProbe{
		FailureThreshold: hc.Retries,
	}

	if hc.Interval != "" {
		if d, err := time.ParseDuration(hc.Interval); err == nil {
			probe.PeriodSeconds = int(d.Seconds())
		}
	}
	if hc.Timeout != "" {
		if d, err := time.ParseDuration(hc.Timeout); err == nil {
			probe.TimeoutSeconds = int(d.Seconds())
		}
	}
	if hc.StartPeriod != "" {
		if d, err := time.ParseDuration(hc.StartPeriod); err == nil {
			probe.InitialDelaySeconds = int(d.Seconds())
		}
	}

	// Parse test command
	var command []string
	if hc.Test.Kind == yaml.ScalarNode {
		command = []string{"/bin/sh", "-c", hc.Test.Value}
	} else if hc.Test.Kind == yaml.SequenceNode {
		for _, n := range hc.Test.Content {
			command = append(command, n.Value)
		}
		// Docker compose tests often start with ["CMD", ...] or ["CMD-SHELL", ...]
		if len(command) > 0 && (command[0] == "CMD" || command[0] == "CMD-SHELL") {
			if command[0] == "CMD" {
				command = command[1:]
			} else {
				command = []string{"/bin/sh", "-c", command[1]}
			}
		}
	}

	if len(command) > 0 {
		probe.Exec = &state.ExecAction{Command: command}
	}

	return probe
}

// safeExpandEnv expands ${VAR} references in data using only the explicit
// allow-list of key→value mappings. Unknown variables are replaced with an
// empty string rather than leaking host environment variables.
func safeExpandEnv(data string, allowed map[string]string) string {
	return os.Expand(data, func(key string) string {
		if val, ok := allowed[key]; ok {
			return val
		}
		return ""
	})
}
