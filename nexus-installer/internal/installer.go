package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Mode          string
	RedisBackend  string
	RegistryType  string
	RegistryURL   string
	RegistryUser  string
	RegistryPass  string
	EnginePort    string
	AgentPort     string
	K8sNamespace  string
	RedisURL      string
	NodeAgentAddr string
}

// detectPkgManager mimics the logic in setup.sh
func detectPkgManager() string {
	if _, err := RunCommand("command -v apt-get"); err == nil {
		return "apt"
	}
	if _, err := RunCommand("command -v dnf"); err == nil {
		return "dnf"
	}
	if _, err := RunCommand("command -v yum"); err == nil {
		return "yum"
	}
	if _, err := RunCommand("command -v pacman"); err == nil {
		return "pacman"
	}
	if _, err := RunCommand("command -v zypper"); err == nil {
		return "zypper"
	}
	return "unknown"
}

// InitializeInstaller handles Phase 0: sudo check and log init
func InitializeInstaller() (string, error) {
	// Check if sudo is already authenticated (should be via build-installer.sh)
	if _, err := RunCommand("sudo -n true 2>/dev/null"); err != nil {
		return "", fmt.Errorf("sudo privileges required. Please run via build-installer.sh or run 'sudo -v' first")
	}
	// Init log file
	_, err := RunCommand("sudo touch /var/log/nexus-install.log && sudo chmod 666 /var/log/nexus-install.log")
	if err != nil {
		return "", fmt.Errorf("failed to init log file: %w", err)
	}
	return "Sudo authenticated & logs initialized", nil
}

// resolvePkg maps logical names to distro-specific packages
func resolvePkg(mgr, pkg string) string {
	switch mgr {
	case "apt":
		switch pkg {
		case "redis":
			return "redis-server"
		case "wireguard":
			return "wireguard wireguard-tools"
		case "ca-certs":
			return "ca-certificates"
		case "golang":
			return "golang-go"
		case "rust":
			return "rustc"
		case "cargo":
			return "cargo"
		}
	case "dnf", "yum":
		switch pkg {
		case "ca-certs":
			return "ca-certificates"
		case "wireguard":
			return "wireguard-tools"
		case "build-essential":
			return "development-tools" // logical group
		case "rust":
			return "rust"
		case "cargo":
			return "cargo"
		}
	case "pacman":
		switch pkg {
		case "ca-certs":
			return "ca-certificates"
		case "wireguard":
			return "wireguard-tools"
		case "golang":
			return "go"
		case "rust":
			return "rust"
		case "cargo":
			return "" // Arch 'rust' package already includes cargo
		case "protobuf":
			return "protobuf"
		}
	}
	// Fallback mappings
	if mgr == "apt" && pkg == "protobuf" {
		return "protobuf-compiler"
	}
	if (mgr == "dnf" || mgr == "yum") && pkg == "protobuf" {
		return "protobuf-compiler"
	}
	return pkg
}

// InstallPackages handles Phase 1 with distro detection
func InstallPackages(backend string) (string, error) {
	mgr := detectPkgManager()
	if mgr == "unknown" {
		return "", fmt.Errorf("unsupported package manager")
	}

	// golang, rust, cargo, protobuf are excluded to speed up server/VM installation.
	// They will be dynamically installed in Phase 7 fallback only if prebuilt download fails.
	logicalPkgs := []string{"curl", "wget", "jq", "git", "ca-certs", "iptables", "ipset", "wireguard", "bash-completion"}
	if backend == "host" {
		logicalPkgs = append(logicalPkgs, "redis")
	}

	var resolved []string
	for _, lp := range logicalPkgs {
		p := resolvePkg(mgr, lp)
		if p != "" {
			resolved = append(resolved, p)
		}
	}

	pkgStr := ""
	for _, p := range resolved {
		pkgStr += p + " "
	}

	var cmd string
	switch mgr {
	case "apt":
		cmd = fmt.Sprintf("sudo DEBIAN_FRONTEND=noninteractive apt-get update -y && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y %s", pkgStr)
	case "dnf":
		cmd = fmt.Sprintf("sudo dnf install -y %s", pkgStr)
	case "yum":
		cmd = fmt.Sprintf("sudo yum install -y %s", pkgStr)
	case "pacman":
		cmd = fmt.Sprintf("sudo pacman -S --noconfirm --needed %s", pkgStr)
	case "zypper":
		cmd = fmt.Sprintf("sudo zypper install -y %s", pkgStr)
	}

	return RunCommand(cmd)
}

// InstallCompilers installs compilers needed for local build fallback.
func InstallCompilers() error {
	mgr := detectPkgManager()
	if mgr == "unknown" {
		return fmt.Errorf("unsupported package manager")
	}

	logicalPkgs := []string{"golang", "rust", "cargo", "protobuf"}
	var resolved []string
	for _, lp := range logicalPkgs {
		p := resolvePkg(mgr, lp)
		if p != "" {
			resolved = append(resolved, p)
		}
	}

	pkgStr := ""
	for _, p := range resolved {
		pkgStr += p + " "
	}

	var cmd string
	switch mgr {
	case "apt":
		cmd = fmt.Sprintf("sudo DEBIAN_FRONTEND=noninteractive apt-get install -y %s", pkgStr)
	case "dnf":
		cmd = fmt.Sprintf("sudo dnf install -y %s", pkgStr)
	case "yum":
		cmd = fmt.Sprintf("sudo yum install -y %s", pkgStr)
	case "pacman":
		cmd = fmt.Sprintf("sudo pacman -S --noconfirm --needed %s", pkgStr)
	case "zypper":
		cmd = fmt.Sprintf("sudo zypper install -y %s", pkgStr)
	}

	_, err := RunCommand(cmd)
	return err
}

// InstallK3s handles Phase 2
func InstallK3s(namespace string) (string, error) {
	out := ""
	if _, err := RunCommand("command -v k3s"); err != nil {
		o, err := RunCommand("curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC=\"--disable=traefik\" sh -")
		if err != nil {
			return o, err
		}
		out += o
	}
	o, err := RunCommand(fmt.Sprintf("sudo /usr/local/bin/k3s kubectl create namespace %s --dry-run=client -o yaml | sudo /usr/local/bin/k3s kubectl apply -f -", namespace))
	return out + o, err
}

// InstallNerdctl handles Phase 3
func InstallNerdctl(user string) (string, error) {
	out := ""
	if _, err := RunCommand("command -v nerdctl"); err != nil {
		arch := "amd64"
		if out, err := RunCommand("uname -m"); err == nil && strings.TrimSpace(out) == "aarch64" {
			arch = "arm64"
		}
		cmd := fmt.Sprintf(`NV="1.7.6"; NA="%s"; TMPDIR=$(mktemp -d); 
		curl -fsSL "https://github.com/containerd/nerdctl/releases/download/v${NV}/nerdctl-full-${NV}-linux-${NA}.tar.gz" -o "$TMPDIR/nerdctl.tar.gz";
		sudo tar -xzf "$TMPDIR/nerdctl.tar.gz" -C /usr/local bin/nerdctl;
		rm -rf "$TMPDIR"`, arch)
		o, err := RunCommand(cmd)
		if err != nil {
			return o, err
		}
		out += o
	}
	RunCommand("sudo ln -sf /usr/local/bin/nerdctl /usr/bin/nerdctl")
	RunCommand(fmt.Sprintf("sudo groupadd -f nexus && sudo usermod -aG nexus %s", user))
	o2, err := RunCommand("sudo chown root:nexus /run/k3s/containerd/containerd.sock && sudo chmod 660 /run/k3s/containerd/containerd.sock")
	out += o2

	// BuildKit Setup (Ported from setup.sh Phase 3.5)
	if _, err := RunCommand("systemctl is-active buildkit"); err != nil {
		bkSvc := `[Unit]
Description=BuildKit
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/buildkitd
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target`
		os.WriteFile("/tmp/buildkit.service", []byte(bkSvc), 0644)
		RunCommand("sudo mv /tmp/buildkit.service /etc/systemd/system/")
		RunCommand("sudo restorecon -v /etc/systemd/system/buildkit.service")
		RunCommand("sudo systemctl daemon-reload")
		RunCommand("sudo systemctl enable --now buildkit")
		out += "BuildKit configured and started\n"
	}

	return out, err
}

// SetupRegistry handles Phase 4
func SetupRegistry(regType, regURL, user, pass string) (string, error) {
	switch regType {
	case "local":
		if _, err := RunCommand("sudo /usr/local/bin/nerdctl --address /run/k3s/containerd/containerd.sock ps -a | grep nexus-registry"); err == nil {
			return "Registry already exists, skipping...", nil
		}
		return RunCommand("sudo /usr/local/bin/nerdctl --address /run/k3s/containerd/containerd.sock run -d --name nexus-registry --restart always -p 5000:5000 registry:2")
	case "dockerhub", "ghcr":
		if user != "" && pass != "" {
			host := "docker.io"
			if regType == "ghcr" {
				host = "ghcr.io"
			}
			return RunCommand(fmt.Sprintf("echo %s | sudo /usr/local/bin/nerdctl --address /run/k3s/containerd/containerd.sock login %s -u %s --password-stdin", pass, host, user))
		}
	}
	return "No registry auth required", nil
}

// InstallRedis handles Phase 5
func InstallRedis(backend, url string) (string, error) {
	if backend == "nerdctl" {
		if _, err := RunCommand("sudo /usr/local/bin/nerdctl --address /run/k3s/containerd/containerd.sock ps -a | grep nexus-redis"); err == nil {
			return "Redis already exists, skipping...", nil
		}
		return RunCommand("sudo /usr/local/bin/nerdctl --address /run/k3s/containerd/containerd.sock run -d --name nexus-redis --restart always -p 6379:6379 redis:7-alpine")
	}
	svc := "redis"
	if _, err := RunCommand("systemctl list-unit-files redis-server.service"); err == nil {
		svc = "redis-server"
	}
	return RunCommand(fmt.Sprintf("sudo systemctl enable %s && sudo systemctl start %s", svc, svc))
}

// SetupWireGuard handles Phase 6
func SetupWireGuard() (string, error) {
	if _, err := os.Stat("/etc/wireguard/wg0.conf"); err == nil {
		return "WireGuard config already exists", nil
	}
	cmd := `sudo mkdir -p /etc/wireguard && sudo chmod 700 /etc/wireguard;
	WG_KEY=$(wg genkey);
	WG_PUB=$(echo "$WG_KEY" | wg pubkey);
	echo "[Interface]
Address = 10.8.0.1/24
ListenPort = 51820
PrivateKey = $WG_KEY
PostUp = iptables -A FORWARD -i wg0 -j ACCEPT; iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
PostDown = iptables -D FORWARD -i wg0 -j ACCEPT; iptables -t nat -D POSTROUTING -o eth0 -j MASQUERADE" | sudo tee /etc/wireguard/wg0.conf > /dev/null;
	sudo chmod 600 /etc/wireguard/wg0.conf;
	sudo systemctl enable wg-quick@wg0 && sudo systemctl start wg-quick@wg0;
	echo $WG_PUB`
	return RunCommand(cmd)
}

var Version = "v0.1.1"

// BuildAndInstallBinaries handles Phase 7.
func BuildAndInstallBinaries(repoRoot string) (string, error) {
	out := ""
	arch := DetectArch()
	releaseTag := Version
	if strings.HasSuffix(Version, "-dev") {
		releaseTag = "latest-dev"
	}
	baseURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/abhi-vmlinuz%%2Fnexus-oss/packages/generic/nexus-oss/%s", releaseTag)

	binaries := []string{"nexus-engine", "nexus", "nexus-node-agent"}
	binaryMap := map[string]string{
		"nexus-engine":     "nexus-engine",
		"nexus":            "nexus-cli",
		"nexus-node-agent": "nexus-node-agent",
	}

	// 1. Try Hybrid Path (Download)
	downloaded := true
	tmpDir := "/tmp/nexus-install"
	os.MkdirAll(tmpDir, 0755)

	out += fmt.Sprintf("Attempting to download prebuilt binaries for %s...\n", arch)
	
	// Download checksums.txt first
	checksumsURL := baseURL + "/checksums.txt"
	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	if err := DownloadFile(checksumsURL, checksumsPath); err != nil {
		out += fmt.Sprintf("Download failed (checksums): %v. Falling back to local build.\n", err)
		downloaded = false
	}

	if downloaded {
		for _, bin := range binaries {
			artifactName := fmt.Sprintf("%s-linux-%s", binaryMap[bin], arch)
			destPath := filepath.Join(tmpDir, bin)
			url := fmt.Sprintf("%s/%s", baseURL, artifactName)

			if err := DownloadFile(url, destPath); err != nil {
				out += fmt.Sprintf("Download failed (%s): %v. Falling back to local build.\n", bin, err)
				downloaded = false
				break
			}

			if err := VerifyChecksum(destPath, checksumsPath, artifactName); err != nil {
				out += fmt.Sprintf("Checksum verification failed (%s): %v. Falling back to local build.\n", bin, err)
				downloaded = false
				break
			}
		}
	}

	// 2. Install Binaries
	if downloaded {
		out += "Installing prebuilt binaries...\n"
		for _, bin := range binaries {
			src := filepath.Join(tmpDir, bin)
			dest := filepath.Join("/usr/local/bin", bin)
			if _, err := RunCommand(fmt.Sprintf("sudo mv %s %s && sudo chmod +x %s", src, dest, dest)); err != nil {
				return out, fmt.Errorf("failed to install %s: %w", bin, err)
			}
		}
	} else {
		// ── Fallback: Local Build ─────────────────────────────────────────────
		out += "Starting local build fallback...\n"
		out += "Installing required compiler packages (Go, Rust, Protobuf) for local compilation...\n"
		if err := InstallCompilers(); err != nil {
			return out, fmt.Errorf("failed to install compilers: %w", err)
		}

		// Paths
		enginePath := filepath.Join(repoRoot, "nexus-engine")
		cliPath := filepath.Join(repoRoot, "nexus-cli")
		agentPath := filepath.Join(repoRoot, "nexus-node-agent")

		// Build Engine
		_, err := RunCommand(fmt.Sprintf("cd %s && go build -o /tmp/nexus-engine ./cmd && sudo mv /tmp/nexus-engine /usr/local/bin/", enginePath))
		if err != nil {
			return out, fmt.Errorf("engine build failed: %w", err)
		}
		out += "Nexus Engine built and installed locally\n"

		// Build CLI
		_, err = RunCommand(fmt.Sprintf("cd %s && go build -o /tmp/nexus . && sudo mv /tmp/nexus /usr/local/bin/", cliPath))
		if err != nil {
			return out, fmt.Errorf("cli build failed: %w", err)
		}
		out += "Nexus CLI built and installed locally\n"

		// Build Agent (Rust)
		_, err = RunCommand(fmt.Sprintf("cd %s && cargo build --release && sudo mv target/release/nexus-node-agent /usr/local/bin/", agentPath))
		if err != nil {
			return out, fmt.Errorf("agent build failed: %w", err)
		}
		out += "Nexus Node Agent built and installed locally\n"
	}

	// Create symlinks in /usr/bin to resolve sudo secure_path issues on RHEL/CentOS/Rocky
	for _, bin := range binaries {
		RunCommand(fmt.Sprintf("sudo ln -sf /usr/local/bin/%s /usr/bin/%s", bin, bin))
	}

	// Restore SELinux contexts
	RestoreSELinux([]string{"/usr/local/bin/nexus", "/usr/local/bin/nexus-engine", "/usr/local/bin/nexus-node-agent"})

	return out, nil
}

// WriteConfigFile handles the Nexus config JSON
func WriteConfigFile(home string, conf Config) (string, error) {
	dir := filepath.Join(home, ".config", "nexus")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}

	content := fmt.Sprintf(`{
  "engine": {
    "url": "http://localhost:%s",
    "mode": "%s"
  },
  "registry": {
    "type": "%s",
    "url": "%s",
    "auth": {
      "type": "none",
      "username": "%s",
      "password": "%s"
    }
  },
  "redis": {
    "url": "%s"
  },
  "node_agent": {
    "addr": "%s"
  },
  "k8s": {
    "namespace": "%s"
  }
}`, conf.EnginePort, conf.Mode, conf.RegistryType, conf.RegistryURL, conf.RegistryUser, conf.RegistryPass, conf.RedisURL, conf.NodeAgentAddr, conf.K8sNamespace)

	err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0600)
	if err != nil {
		return "", err
	}

	// Change ownership of the target user's config directory to the non-root user
	user := os.Getenv("USER")
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		user = sudoUser
	}
	RunCommand(fmt.Sprintf("sudo chown -R %s:%s %s", user, user, dir))

	// If running as root, and the target home is not /root, also write to /root for convenience
	if os.Getuid() == 0 && home != "/root" {
		rootDir := "/root/.config/nexus"
		if err := os.MkdirAll(rootDir, 0700); err == nil {
			_ = os.WriteFile(filepath.Join(rootDir, "config.json"), []byte(content), 0600)
		}
	}

	// Also write the system-wide environment configuration for the systemd services
	etcDir := "/etc/nexus"
	if err := os.MkdirAll(etcDir, 0755); err != nil {
		return "", err
	}

	insecure := "true"
	if conf.Mode == "prod" {
		insecure = "false"
	}

	envContent := fmt.Sprintf(`NEXUS_MODE=%s
NEXUS_PORT=%s
NEXUS_REDIS_URL=%s
NEXUS_K3S_NAMESPACE=%s
NEXUS_REGISTRY_URL=%s
NEXUS_REGISTRY_AUTH_TYPE=%s
NEXUS_REGISTRY_AUTH_USERNAME=%s
NEXUS_REGISTRY_AUTH_PASSWORD=%s
NEXUS_NODE_AGENT_ADDR=%s
NEXUS_NODE_AGENT_INSECURE=%s
`, conf.Mode, conf.EnginePort, conf.RedisURL, conf.K8sNamespace, conf.RegistryURL, conf.RegistryType, conf.RegistryUser, conf.RegistryPass, conf.NodeAgentAddr, insecure)

	if err := os.WriteFile(filepath.Join(etcDir, "engine.env"), []byte(envContent), 0600); err != nil {
		return "", err
	}

	return "Configuration written to " + filepath.Join(dir, "config.json") + " and " + filepath.Join(etcDir, "engine.env"), nil
}

// SetupMTLSCertificates handles mTLS generation
func SetupMTLSCertificates(mode string) (string, error) {
	if mode == "dev" {
		return "Dev mode: skipping mTLS cert generation", nil
	}

	certDir := "/etc/nexus"
	RunCommand("sudo mkdir -p " + certDir)

	// Generate CA
	RunCommand(fmt.Sprintf("sudo openssl req -x509 -newkey rsa:4096 -days 3650 -nodes -keyout %s/ca.key -out %s/ca.crt -subj '/CN=Nexus Root CA'", certDir, certDir))

	// Generate Server Cert (Node Agent)
	RunCommand(fmt.Sprintf("sudo openssl req -newkey rsa:4096 -nodes -keyout %s/agent-server.key -out %s/agent-server.csr -subj '/CN=localhost'", certDir, certDir))
	RunCommand(fmt.Sprintf("sudo openssl x509 -req -in %s/agent-server.csr -CA %s/ca.crt -CAkey %s/ca.key -CAcreateserial -out %s/agent-server.crt -days 365", certDir, certDir, certDir, certDir))

	// Generate Client Cert (Engine)
	RunCommand(fmt.Sprintf("sudo openssl req -newkey rsa:4096 -nodes -keyout %s/agent-client.key -out %s/agent-client.csr -subj '/CN=nexus-engine'", certDir, certDir))
	RunCommand(fmt.Sprintf("sudo openssl x509 -req -in %s/agent-client.csr -CA %s/ca.crt -CAkey %s/ca.key -CAcreateserial -out %s/agent-client.crt -days 365", certDir, certDir, certDir, certDir))

	// Permissions
	RunCommand(fmt.Sprintf("sudo chmod 600 %s/*.key %s/*.crt", certDir, certDir))

	return "mTLS certificates generated in " + certDir, nil
}

// SetupServices handles systemd setup
func SetupServices(mode, port, redisURL, regURL, agentAddr, namespace string) (string, error) {
	insecure := "true"
	if mode == "prod" {
		insecure = "false"
	}

	agentSvc := fmt.Sprintf(`[Unit]
Description=Nexus OSS Node Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/nexus-node-agent
Restart=on-failure
Environment=NEXUS_MODE=%s
Environment=NODE_AGENT_LISTEN_ADDR=0.0.0.0:50051
Environment=NODE_AGENT_INSECURE=%s
Environment=NODE_AGENT_TLS_CERT=/etc/nexus/agent-server.crt
Environment=NODE_AGENT_TLS_KEY=/etc/nexus/agent-server.key
Environment=NODE_AGENT_CA_CERT=/etc/nexus/ca.crt
AmbientCapabilities=CAP_NET_ADMIN
CapabilityBoundingSet=CAP_NET_ADMIN

[Install]
WantedBy=multi-user.target`, mode, insecure)

	engineSvc := fmt.Sprintf(`[Unit]
Description=Nexus OSS Engine
Wants=k3s.service
After=network.target redis.service nexus-node-agent.service k3s.service

[Service]
Type=simple
ExecStart=/usr/local/bin/nexus-engine
Restart=on-failure
RestartSec=5
EnvironmentFile=-/etc/nexus/engine.env
Environment=KUBECONFIG=/etc/rancher/k3s/k3s.yaml
Environment=CONTAINERD_ADDRESS=/run/k3s/containerd/containerd.sock
Environment=CONTAINERD_NAMESPACE=k8s.io

[Install]
WantedBy=multi-user.target`)

	os.WriteFile("/tmp/nexus-node-agent.service", []byte(agentSvc), 0644)
	os.WriteFile("/tmp/nexus-engine.service", []byte(engineSvc), 0644)

	RunCommand("sudo mv /tmp/nexus-node-agent.service /etc/systemd/system/")
	RunCommand("sudo mv /tmp/nexus-engine.service /etc/systemd/system/")
	RunCommand("sudo restorecon -v /etc/systemd/system/nexus-*.service")
	RunCommand("sudo systemctl daemon-reload")
	RunCommand("sudo systemctl enable nexus-node-agent nexus-engine")
	return RunCommand("sudo systemctl restart nexus-node-agent nexus-engine")
}

// SetupShellCompletion adds the nexus completion command to the user's shell profile.
func SetupShellCompletion(home string) (string, error) {
	var summary []string

	// 1. Bash Completion
	if bashScript, err := RunCommand("/usr/local/bin/nexus completion bash"); err == nil && len(strings.TrimSpace(bashScript)) > 100 {
		tmpFile := "/tmp/nexus-completion-bash"
		if err := os.WriteFile(tmpFile, []byte(bashScript), 0644); err == nil {
			// Try modern path first
			if _, err := RunCommand("sudo mkdir -p /usr/share/bash-completion/completions"); err == nil {
				if _, err := RunCommand(fmt.Sprintf("sudo mv %s /usr/share/bash-completion/completions/nexus && sudo chmod 644 /usr/share/bash-completion/completions/nexus", tmpFile)); err == nil {
					summary = append(summary, "Bash (system-wide)")
				}
			}
			// Try legacy fallback if modern path was not written
			if len(summary) == 0 {
				if _, err := RunCommand("sudo mkdir -p /etc/bash_completion.d"); err == nil {
					if _, err := RunCommand(fmt.Sprintf("sudo mv %s /etc/bash_completion.d/nexus && sudo chmod 644 /etc/bash_completion.d/nexus", tmpFile)); err == nil {
						summary = append(summary, "Bash (legacy)")
					}
				}
			}
			os.Remove(tmpFile) // Clean up temp file if still there
		}
	} else if err != nil {
		// Log error
		RunCommand(fmt.Sprintf("echo 'Bash completion generation failed: %s' | sudo tee -a /var/log/nexus-install.log > /dev/null", strings.ReplaceAll(err.Error(), "'", "'\\''")))
	}

	// 2. Zsh Completion
	if zshScript, err := RunCommand("/usr/local/bin/nexus completion zsh"); err == nil && len(strings.TrimSpace(zshScript)) > 100 {
		tmpFile := "/tmp/nexus-completion-zsh"
		if err := os.WriteFile(tmpFile, []byte(zshScript), 0644); err == nil {
			if _, err := RunCommand("sudo mkdir -p /usr/share/zsh/vendor-completions"); err == nil {
				if _, err := RunCommand(fmt.Sprintf("sudo mv %s /usr/share/zsh/vendor-completions/_nexus && sudo chmod 644 /usr/share/zsh/vendor-completions/_nexus", tmpFile)); err == nil {
					summary = append(summary, "Zsh (system-wide)")
				}
			}
			os.Remove(tmpFile)
		}
	} else if err != nil {
		RunCommand(fmt.Sprintf("echo 'Zsh completion generation failed: %s' | sudo tee -a /var/log/nexus-install.log > /dev/null", strings.ReplaceAll(err.Error(), "'", "'\\''")))
	}

	// 3. Fish Completion
	if fishScript, err := RunCommand("/usr/local/bin/nexus completion fish"); err == nil && len(strings.TrimSpace(fishScript)) > 100 {
		tmpFile := "/tmp/nexus-completion-fish"
		if err := os.WriteFile(tmpFile, []byte(fishScript), 0644); err == nil {
			if _, err := RunCommand("sudo mkdir -p /usr/share/fish/vendor_completions.d"); err == nil {
				if _, err := RunCommand(fmt.Sprintf("sudo mv %s /usr/share/fish/vendor_completions.d/nexus.fish && sudo chmod 644 /usr/share/fish/vendor_completions.d/nexus.fish", tmpFile)); err == nil {
					summary = append(summary, "Fish (system-wide)")
				}
			}
			os.Remove(tmpFile)
		}
	} else if err != nil {
		RunCommand(fmt.Sprintf("echo 'Fish completion generation failed: %s' | sudo tee -a /var/log/nexus-install.log > /dev/null", strings.ReplaceAll(err.Error(), "'", "'\\''")))
	}

	if len(summary) > 0 {
		return fmt.Sprintf("Shell completions installed system-wide: %s", strings.Join(summary, ", ")), nil
	}

	return "No shell completions were installed (framework directories missing or generation failed)", nil
}

// SetupNetworkPolicies applies the appropriate NetworkPolicy to the K3s cluster.
func SetupNetworkPolicies(mode, namespace string) (string, error) {
	var yaml string
	if mode == "prod" {
		yaml = fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: nexus-prod-isolate
  namespace: %s
  labels:
    managed-by: nexus
    mode: prod
spec:
  podSelector:
    matchLabels:
      app: nexus-challenge
  ingress:
    - from:
        - ipBlock:
            cidr: 10.8.0.0/24
  egress:
    - {}
  policyTypes:
    - Ingress
    - Egress`, namespace)
	} else {
		yaml = fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: nexus-dev-allow-all
  namespace: %s
  labels:
    managed-by: nexus
    mode: dev
spec:
  podSelector: {}
  ingress:
    - {}
  egress:
    - {}
  policyTypes:
    - Ingress
    - Egress`, namespace)
	}

	cmd := fmt.Sprintf("sudo k3s kubectl apply -f - <<'EOF'\n%s\nEOF", yaml)
	out, err := RunCommand(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to apply network policy: %w", err)
	}

	return fmt.Sprintf("Applied %s network policy to namespace %s:\n%s", mode, namespace, out), nil
}
