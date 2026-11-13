package observe

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMachineFilesAreWrittenWhenOpened(t *testing.T) {
	dir := t.TempDir()
	wrote, err := writeMachineFiles(dir, "amd64", []string{"/etc/passwd", "/sys/devices/system/cpu/possible", "/proc/cpuinfo"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/proc/cpuinfo", "/sys/devices/system/cpu/possible"}
	if !reflect.DeepEqual(wrote, want) {
		t.Fatalf("wrote %v, want %v", wrote, want)
	}
	b, err := os.ReadFile(filepath.Join(dir, "sys/devices/system/cpu/possible"))
	if err != nil || string(b) != "0-63\n" {
		t.Fatalf("possible = %q, %v", b, err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "proc/cpuinfo"))
	if string(b) != "processor\t: 0\n\n" {
		t.Fatalf("cpuinfo = %q", b)
	}
}

func TestMachineFilesAreNotWrittenWhenNotOpened(t *testing.T) {
	dir := t.TempDir()
	wrote, err := writeMachineFiles(dir, "amd64", []string{"/etc/passwd"})
	if err != nil || len(wrote) != 0 {
		t.Fatalf("wrote %v, %v", wrote, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "proc")); err == nil {
		t.Fatal("proc exists")
	}
}

func TestMachineFilesAreNotWrittenOnArm(t *testing.T) {
	dir := t.TempDir()
	wrote, err := writeMachineFiles(dir, "arm64", []string{"/proc/cpuinfo"})
	if err != nil || len(wrote) != 0 {
		t.Fatalf("wrote %v, %v", wrote, err)
	}
}

func TestMachineFilesKeepWhatIsThere(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "proc", "cpuinfo")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("mine"), 0o644)
	wrote, err := writeMachineFiles(dir, "amd64", []string{"/proc/cpuinfo"})
	if err != nil || len(wrote) != 0 {
		t.Fatalf("wrote %v, %v", wrote, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "mine" {
		t.Fatalf("cpuinfo = %q", b)
	}
}

func TestProcessStatFilesAreWrittenForTheFirstThreeProcesses(t *testing.T) {
	dir := t.TempDir()
	wrote, err := writeMachineFiles(dir, "amd64", []string{"/proc/2/stat"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/proc/1/stat", "/proc/2/stat", "/proc/3/stat"}
	if !reflect.DeepEqual(wrote, want) {
		t.Fatalf("wrote %v, want %v", wrote, want)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "proc/2/stat"))
	if got := len(strings.Fields(string(b))); got != 52 {
		t.Fatalf("%d words in %q", got, b)
	}
	if !strings.HasPrefix(string(b), "2 (program) R ") {
		t.Fatalf("stat = %q", b)
	}
}

func TestProcessStatIsNotWrittenForOtherProcFiles(t *testing.T) {
	dir := t.TempDir()
	wrote, _ := writeMachineFiles(dir, "amd64", []string{"/proc/self/stat", "/proc/meminfo", "/proc/2/status"})
	if len(wrote) != 0 {
		t.Fatalf("wrote %v", wrote)
	}
}

func TestSharedMemoryDirectoryIsMadeWhenTheProgramNamedIt(t *testing.T) {
	for _, opened := range [][]string{{"/dev/shm"}, {"/dev/shm/psm_1"}} {
		dir := t.TempDir()
		wrote, err := writeMachineFiles(dir, "arm64", opened)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(wrote, []string{"/dev/shm"}) {
			t.Fatalf("%v: wrote %v", opened, wrote)
		}
		st, err := os.Stat(filepath.Join(dir, "dev", "shm"))
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSticky == 0 || st.Mode().Perm() != 0o777 {
			t.Fatalf("dev/shm = %v %v", st, err)
		}
	}
}

func TestSharedMemoryDirectoryIsNotMadeOtherwise(t *testing.T) {
	dir := t.TempDir()
	wrote, _ := writeMachineFiles(dir, "amd64", []string{"/dev/shmx", "/dev/null", "/dev"})
	if len(wrote) != 0 {
		t.Fatalf("wrote %v", wrote)
	}
	if _, err := os.Lstat(filepath.Join(dir, "dev")); err == nil {
		t.Fatal("dev exists")
	}
}

func TestSharedMemoryDirectoryIsKept(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "dev", "shm"), 0o700)
	wrote, _ := writeMachineFiles(dir, "amd64", []string{"/dev/shm"})
	if len(wrote) != 0 {
		t.Fatalf("wrote %v", wrote)
	}
}
