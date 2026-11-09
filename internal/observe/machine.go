package observe

import (
	"os"
	"path/filepath"
	"strings"
)

// machineFiles are what a program reads to learn what machine it runs on. A container is told by
// the kernel it shares; nanos keeps no /proc/cpuinfo on x86_64 and lists the processors that are
// online but not those the machine could have. MongoDB counts them before it does anything else
// and stops when the answer is none (7 fails an invariant that wants more than none, 8 is built
// with tcmalloc, which reads /sys/devices/system/cpu/possible first).
//
// The processors a machine will be given are said when it is started, not when the package is
// made, so each file is wrong in the direction it can afford. A program reading cpuinfo sizes its
// work by what it counts, and one too few only costs speed. What reads "possible" cannot take a
// number below the truth (tcmalloc keeps a cache per processor, indexed by the processor it runs
// on), and a number above it costs memory never used.
var machineFiles = map[string]string{
	"proc/cpuinfo":                    "processor\t: 0\n\n",
	"sys/devices/system/cpu/possible": "0-63\n",
}

// writeMachineFiles puts into sysroot the machine files that the program opened, on x86_64 only
// (on aarch64 the kernel answers for cpuinfo itself, and a file of ours in its place would stop it
// from making its own). A file the sysroot already has is left alone. It returns the paths it
// wrote, sorted by name.
func writeMachineFiles(sysroot, arch string, opened []string) ([]string, error) {
	if arch != "amd64" && arch != "x86_64" {
		return nil, nil
	}
	var wrote []string
	for _, name := range []string{"proc/cpuinfo", "sys/devices/system/cpu/possible"} {
		if !openedPath(opened, "/"+name) {
			continue
		}
		path := filepath.Join(sysroot, filepath.FromSlash(name))
		if _, err := os.Lstat(path); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return wrote, err
		}
		if err := os.WriteFile(path, []byte(machineFiles[name]), 0o444); err != nil {
			return wrote, err
		}
		wrote = append(wrote, "/"+name)
	}
	return wrote, nil
}

func openedPath(opened []string, want string) bool {
	for _, p := range opened {
		if strings.TrimRight(p, "/") == want {
			return true
		}
	}
	return false
}
