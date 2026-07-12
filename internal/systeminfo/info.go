package systeminfo

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Memory struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	SwapTotalBytes uint64 `json:"swap_total_bytes"`
	SwapFreeBytes  uint64 `json:"swap_free_bytes"`
}
type Disk struct {
	Path       string `json:"path"`
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
}
type NetworkAddress struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
}
type Info struct {
	Hostname      string           `json:"hostname"`
	AgentVersion  string           `json:"agent_version"`
	OS            string           `json:"os"`
	Distribution  string           `json:"distribution,omitempty"`
	Kernel        string           `json:"kernel,omitempty"`
	Architecture  string           `json:"architecture"`
	CPUModel      string           `json:"cpu_model,omitempty"`
	CPUCores      int              `json:"cpu_cores"`
	LoadAverage   string           `json:"load_average,omitempty"`
	Memory        Memory           `json:"memory"`
	Disks         []Disk           `json:"disks"`
	Network       []NetworkAddress `json:"network"`
	UptimeSeconds float64          `json:"uptime_seconds,omitempty"`
	SystemdState  string           `json:"systemd_state,omitempty"`
}

func Collect(version string) (Info, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return Info{}, err
	}
	info := Info{Hostname: hostname, AgentVersion: version, OS: runtime.GOOS, Architecture: runtime.GOARCH, CPUCores: runtime.NumCPU()}
	info.Distribution = osRelease()
	info.Kernel = commandOutput("uname", "-r")
	info.CPUModel = cpuModel()
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(b))
		if len(fields) >= 3 {
			info.LoadAverage = strings.Join(fields[:3], " ")
		}
	}
	info.Memory = memoryInfo()
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err == nil {
		info.Disks = []Disk{{Path: "/", TotalBytes: uint64(stat.Blocks) * uint64(stat.Bsize), FreeBytes: uint64(stat.Bavail) * uint64(stat.Bsize)}}
	}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			info.Network = append(info.Network, NetworkAddress{Interface: iface.Name, Address: addr.String()})
		}
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(b))
		if len(fields) > 0 {
			info.UptimeSeconds, _ = strconv.ParseFloat(fields[0], 64)
		}
	}
	info.SystemdState = commandOutput("systemctl", "is-system-running")
	return info, nil
}

func osRelease() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if value, ok := strings.CutPrefix(s.Text(), "PRETTY_NAME="); ok {
			return strings.Trim(value, "\"")
		}
	}
	return ""
}
func cpuModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err == nil {
		defer f.Close()
		s := bufio.NewScanner(f)
		for s.Scan() {
			if parts := strings.SplitN(s.Text(), ":", 2); len(parts) == 2 && strings.TrimSpace(parts[0]) == "model name" {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return commandOutput("sysctl", "-n", "machdep.cpu.brand_string")
}
func memoryInfo() Memory {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return Memory{}
	}
	values := map[string]uint64{}
	s := bufio.NewScanner(strings.NewReader(string(b)))
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) >= 2 {
			value, _ := strconv.ParseUint(fields[1], 10, 64)
			values[strings.TrimSuffix(fields[0], ":")] = value * 1024
		}
	}
	return Memory{TotalBytes: values["MemTotal"], AvailableBytes: values["MemAvailable"], SwapTotalBytes: values["SwapTotal"], SwapFreeBytes: values["SwapFree"]}
}
func commandOutput(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b, _ := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(b))
}
