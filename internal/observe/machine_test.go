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
	wrote, err := writeMachineFiles(dir, "amd64", "/usr/bin/app", []string{"/etc/passwd", "/sys/devices/system/cpu/possible", "/proc/cpuinfo"})
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
	wrote, err := writeMachineFiles(dir, "amd64", "/usr/bin/app", []string{"/etc/passwd"})
	if err != nil || len(wrote) != 0 {
		t.Fatalf("wrote %v, %v", wrote, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "proc")); err == nil {
		t.Fatal("proc exists")
	}
}

func TestMachineFilesAreNotWrittenOnArm(t *testing.T) {
	dir := t.TempDir()
	wrote, err := writeMachineFiles(dir, "arm64", "/usr/bin/app", []string{"/proc/cpuinfo"})
	if err != nil || len(wrote) != 0 {
		t.Fatalf("wrote %v, %v", wrote, err)
	}
}

func TestMachineFilesKeepWhatIsThere(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "proc", "cpuinfo")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("mine"), 0o644)
	wrote, err := writeMachineFiles(dir, "amd64", "/usr/bin/app", []string{"/proc/cpuinfo"})
	if err != nil || len(wrote) != 0 {
		t.Fatalf("wrote %v, %v", wrote, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "mine" {
		t.Fatalf("cpuinfo = %q", b)
	}
}

func TestProcessStatFilesAreWrittenForTheFirstThreeProcesses(t *testing.T) {
	dir := t.TempDir()
	wrote, err := writeMachineFiles(dir, "amd64", "/usr/bin/app", []string{"/proc/2/stat"})
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
	wrote, _ := writeMachineFiles(dir, "amd64", "/usr/bin/app", []string{"/proc/self/stat", "/proc/meminfo", "/proc/2/status"})
	if len(wrote) != 0 {
		t.Fatalf("wrote %v", wrote)
	}
}

func TestSharedMemoryDirectoryIsMadeWhenTheProgramNamedIt(t *testing.T) {
	for _, opened := range [][]string{{"/dev/shm"}, {"/dev/shm/psm_1"}} {
		dir := t.TempDir()
		wrote, err := writeMachineFiles(dir, "arm64", "/usr/bin/app", opened)
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
	wrote, _ := writeMachineFiles(dir, "amd64", "/usr/bin/app", []string{"/dev/shmx", "/dev/null", "/dev"})
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
	wrote, _ := writeMachineFiles(dir, "amd64", "/usr/bin/app", []string{"/dev/shm"})
	if len(wrote) != 0 {
		t.Fatalf("wrote %v", wrote)
	}
}

func TestSelfStatIsWrittenForNodeOnly(t *testing.T) {
	dir := t.TempDir()
	wrote, err := writeMachineFiles(dir, "amd64", "/usr/local/bin/node", []string{"/etc/passwd"})
	if err != nil || len(wrote) != 1 || wrote[0] != "/proc/self/stat" {
		t.Fatalf("wrote = %v, err = %v", wrote, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "proc", "self", "stat"))
	if err != nil {
		t.Fatal(err)
	}
	words := strings.Fields(string(b))
	if len(words) != 52 || words[23] == "0" {
		t.Errorf("stat = %q, want fifty-two words with the resident memory set", b)
	}
	other := t.TempDir()
	wrote, _ = writeMachineFiles(other, "amd64", "/usr/bin/python3", []string{"/etc/passwd"})
	if len(wrote) != 0 {
		t.Errorf("wrote = %v for a program that is not node", wrote)
	}
}

func TestSelfStatKeepsAFileOfTheSysrootAndSkipsArm64AndKnowsNodejs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proc", "self", "stat")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("mine\n"), 0o444)
	wrote, err := writeMachineFiles(dir, "amd64", "/usr/bin/node", nil)
	if err != nil || len(wrote) != 0 {
		t.Fatalf("wrote = %v, err = %v, want nothing over an existing file", wrote, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "mine\n" {
		t.Errorf("the file was replaced: %q", b)
	}
	arm := t.TempDir()
	wrote, _ = writeMachineFiles(arm, "arm64", "/usr/bin/node", nil)
	if len(wrote) != 0 {
		t.Errorf("wrote = %v on arm64", wrote)
	}
	if _, err := os.Lstat(filepath.Join(arm, "proc", "self", "stat")); err == nil {
		t.Error("proc/self/stat exists on arm64")
	}
	js := t.TempDir()
	wrote, _ = writeMachineFiles(js, "amd64", "/usr/bin/nodejs", nil)
	if len(wrote) != 1 || wrote[0] != "/proc/self/stat" {
		t.Errorf("wrote = %v for nodejs", wrote)
	}
}

// nanos has no /tmp, and PHP (OPcache) makes its lock file there at the start: the package carries an
// empty one, with the mode of /tmp
func TestTmpDirectoryIsMadeWhenThePackageHasNone(t *testing.T) {
	dir := t.TempDir()
	made, err := carryTmp(dir)
	if err != nil || !made {
		t.Fatalf("carryTmp = %v, %v; want true", made, err)
	}
	st, err := os.Stat(filepath.Join(dir, "tmp"))
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSticky == 0 || st.Mode().Perm() != 0o777 {
		t.Fatalf("tmp = %v %v", st, err)
	}
	// the second time it is there
	if made, err := carryTmp(dir); err != nil || made {
		t.Fatalf("second carryTmp = %v, %v; want false", made, err)
	}
}

func TestTmpDirectoryOfTheImageIsKept(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "tmp"), 0o700)
	if made, err := carryTmp(dir); err != nil || made {
		t.Fatalf("carryTmp = %v, %v; want false", made, err)
	}
	if st, _ := os.Stat(filepath.Join(dir, "tmp")); st.Mode().Perm() != 0o700 {
		t.Errorf("the mode of the directory of the image was changed to %v", st.Mode())
	}
}
