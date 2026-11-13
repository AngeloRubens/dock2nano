package trace

import (
	"os"
	"slices"
	"testing"
)

// The files in testdata are what strace wrote for real, in the integration job:
// nginx:1.27-alpine, and an image whose entrypoint is a shell script that
// execs busybox httpd.

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRealNginxTrace(t *testing.T) {
	raw := readFixture(t, "nginx.strace")
	final, ok := FinalExec(raw)
	if !ok || final.Path != "/usr/sbin/nginx" || final.Pid != "9" {
		t.Fatalf("final program %+v, %v", final, ok)
	}
	if !slices.Equal(final.Argv, []string{"nginx", "-g", "daemon off;"}) {
		t.Errorf("argv %q", final.Argv)
	}
	paths, executed := TouchedPaths(raw, "/", final.Line)
	for _, want := range []string{"/usr/sbin/nginx", "/etc/nginx/nginx.conf"} {
		if !slices.Contains(paths, want) {
			t.Errorf("%s is missing from %q", want, paths)
		}
	}
	// what the entrypoint scripts ran before the server is not carried
	for _, not := range []string{"/usr/bin/find", "/usr/bin/sort", "/usr/bin/basename", "/usr/bin/awk"} {
		if slices.Contains(paths, not) || slices.Contains(executed, not) {
			t.Errorf("%s was carried", not)
		}
	}
	if !slices.Contains(executed, "/usr/sbin/nginx") {
		t.Errorf("the program is not in executed: %q", executed)
	}
}

func TestRealEntrypointScriptTrace(t *testing.T) {
	raw := readFixture(t, "entrypoint-script.strace")
	final, ok := FinalExec(raw)
	if !ok || final.Path != "/usr/sbin/httpd" || final.Pid != "9" {
		t.Fatalf("final program %+v, %v", final, ok)
	}
	if !slices.Equal(final.Argv, []string{"httpd", "-f", "-p", "8080", "-h", "/www"}) {
		t.Errorf("argv %q", final.Argv)
	}
	paths, _ := TouchedPaths(raw, "/", final.Line)
	// index.html is opened with a relative name, after chdir("/www")
	if !slices.Contains(paths, "/www/index.html") {
		t.Errorf("/www/index.html is missing from %q", paths)
	}
	if slices.Contains(paths, "/entry.sh") {
		t.Errorf("the entrypoint script was carried: %q", paths)
	}
	// execve calls that failed with ENOENT find nothing
	if slices.Contains(paths, "/usr/local/sbin/httpd") {
		t.Errorf("a failed execve was carried: %q", paths)
	}
}
