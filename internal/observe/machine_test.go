package observe

import (
	"os"
	"path/filepath"
	"reflect"
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
