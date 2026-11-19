package observe

import (
	"os"
	"path/filepath"
	"strconv"
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
//
// The process files are for the same programs: mongodb reads /proc/<pid>/stat of the first few
// processes for its diagnostics and logs an assertion every second when it cannot. A number is
// the one the program had in the container, which is not the one it will have, so the first three
// are written. The line has fifty-two words, since a reader takes the twenty-fourth for the
// resident memory without counting them first.
var machineFiles = map[string]string{
	"proc/cpuinfo":                    "processor\t: 0\n\n",
	"sys/devices/system/cpu/possible": "0-63\n",
}

// sharedMemory is the directory programs put shared memory in (python's multiprocessing makes its
// arenas there, and asks the file system about it first). nanos has none; an empty directory in the
// package is enough, which the package of a program that uses it was tested with.
const sharedMemory = "/dev/shm"

// usesSharedMemory says whether the program named /dev/shm or something under it.
func usesSharedMemory(opened []string) bool {
	for _, p := range opened {
		if p == sharedMemory || strings.HasPrefix(p, sharedMemory+"/") {
			return true
		}
	}
	return false
}

// writeMachineFiles puts into sysroot the machine files that the program opened, on x86_64 only
// (on aarch64 the kernel answers for cpuinfo itself, and a file of ours in its place would stop it
// from making its own). A file the sysroot already has is left alone. It returns the paths it
// wrote.
func writeMachineFiles(sysroot, arch string, opened []string) ([]string, error) {
	var wrote []string
	// the directory of shared memory is wanted on any architecture
	if usesSharedMemory(opened) {
		dir := filepath.Join(sysroot, "dev", "shm")
		if _, err := os.Lstat(dir); err != nil {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return wrote, err
			}
			if err := os.Chmod(dir, os.ModeSticky|0o777); err != nil {
				return wrote, err
			}
			wrote = append(wrote, sharedMemory)
		}
	}
	if arch != "amd64" && arch != "x86_64" {
		return wrote, nil
	}
	for _, name := range []string{"proc/cpuinfo", "sys/devices/system/cpu/possible", "proc/1/stat", "proc/2/stat", "proc/3/stat"} {
		if strings.HasPrefix(name, "proc/") && strings.HasSuffix(name, "/stat") {
			if !openedProcessStat(opened) {
				continue
			}
		} else if !openedPath(opened, "/"+name) {
			continue
		}
		path := filepath.Join(sysroot, filepath.FromSlash(name))
		if _, err := os.Lstat(path); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return wrote, err
		}
		says, ok := machineFiles[name]
		if !ok {
			says = processStat(strings.Split(name, "/")[1])
		}
		if err := os.WriteFile(path, []byte(says), 0o444); err != nil {
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

// openedProcessStat says whether the program opened /proc/<number>/stat of some process.
func openedProcessStat(opened []string) bool {
	for _, p := range opened {
		parts := strings.Split(p, "/")
		if len(parts) == 4 && parts[0] == "" && parts[1] == "proc" && parts[3] == "stat" {
			if _, err := strconv.Atoi(parts[2]); err == nil {
				return true
			}
		}
	}
	return false
}

// processStat is the line a process of Linux has in /proc/<pid>/stat: fifty-two words. The times
// are zero, which stops being true the moment the process runs.
func processStat(pid string) string {
	words := []string{pid, "(program)", "R", "0", pid, pid}
	for len(words) < 52 {
		words = append(words, "0")
	}
	return strings.Join(words, " ") + "\n"
}
