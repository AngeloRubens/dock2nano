//go:build integration

package observe_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AngeloRubens/dock2nano/internal/docker"
	"github.com/AngeloRubens/dock2nano/internal/observe"
)

func needDocker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "version").CombinedOutput(); err != nil {
		t.Skipf("docker version failed: %v: %s", err, out)
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// keepTrace is where the raw strace output of a run goes when OBSERVE_TRACE_DIR is set (the
// workflow uploads that directory), and "" otherwise.
func keepTrace(t *testing.T, name string) string {
	t.Helper()
	dir := os.Getenv("OBSERVE_TRACE_DIR")
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name+".strace")
}

// containerIDs lists all containers, running or not.
func containerIDs(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-a", "-q", "--no-trunc").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	ids := strings.Fields(string(out))
	slices.Sort(ids)
	return ids
}

// privateTemp points TMPDIR at a directory of its own, so that the temporary
// directories Observe makes can be told from all others.
func privateTemp(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "d2n-observe-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMPDIR", dir)
	return dir
}

// list gives the regular files and links of the sysroot as image paths, with
// their sizes.
func list(t *testing.T, sysroot string) (map[string]int64, int64) {
	t.Helper()
	files := map[string]int64{}
	var total int64
	err := filepath.WalkDir(sysroot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(sysroot, p)
		if err != nil {
			return err
		}
		files["/"+filepath.ToSlash(rel)] = info.Size()
		total += info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("walking the sysroot: %v", err)
	}
	return files, total
}

func logSysroot(t *testing.T, files map[string]int64, total int64) {
	t.Helper()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString("\n  ")
		b.WriteString(n)
	}
	t.Logf("sysroot: %d files, %d bytes:%s", len(files), total, b.String())
}

// checkNoLeftovers fails when containers or temporary directories are left.
func checkNoLeftovers(t *testing.T, before []string, tmp string) {
	t.Helper()
	after := containerIDs(t)
	// a container that existed before and is gone now is not ours; one that is new is
	var added []string
	for _, id := range after {
		if !slices.Contains(before, id) {
			added = append(added, id)
		}
	}
	if len(added) > 0 {
		t.Errorf("containers before %v, after %v: Observe left %v", before, after, added)
	}
	left, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range left {
		t.Errorf("temporary entry left after the sysroot was removed: %s", e.Name())
	}
}

func TestObserveNginx(t *testing.T) {
	needDocker(t)
	tmp := privateTemp(t)
	before := containerIDs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := observe.Observe(ctx, docker.Default(), observe.Options{
		Image:     "nginx:1.27-alpine",
		Pull:      true,
		Seconds:   30,
		Log:       t.Logf,
		KeepTrace: keepTrace(t, "nginx"),
	})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	removed := false
	removeSysroot := func() {
		if !removed {
			removed = true
			os.RemoveAll(res.Sysroot)
		}
	}
	t.Cleanup(removeSysroot)

	t.Logf("program %s, argv %v, working dir %q", res.Program, res.Argv, res.WorkingDir)
	t.Logf("exposed ports %v, configuration files %v", res.ExposedPorts, res.ConfigurationFiles)
	for _, w := range res.Warnings {
		t.Logf("warning: %s", w)
	}
	files, total := list(t, res.Sysroot)
	logSysroot(t, files, total)

	if !strings.HasSuffix(res.Program, "nginx") {
		t.Errorf("Program = %q, want it to end with nginx", res.Program)
	}
	if !slices.Contains(res.ExposedPorts, 80) {
		t.Errorf("ExposedPorts = %v, want 80 in it", res.ExposedPorts)
	}
	for _, want := range []string{res.Program, "/etc/nginx/nginx.conf"} {
		if _, ok := files[want]; !ok {
			t.Errorf("the sysroot does not hold %s", want)
		}
	}
	for _, not := range []string{"/bin/sh", "/sbin/apk", "/usr/bin/ldd"} {
		if _, ok := files[not]; ok {
			t.Errorf("the sysroot holds %s, which nothing of the program needs", not)
		}
	}
	found := false
	for _, f := range res.ConfigurationFiles {
		if strings.Contains(f, "etc/nginx") {
			found = true
		}
	}
	if !found {
		t.Errorf("ConfigurationFiles = %v, want an /etc/nginx path in it", res.ConfigurationFiles)
	}

	// the caller removes the sysroot; then nothing of Observe is left
	removeSysroot()
	checkNoLeftovers(t, before, tmp)
}

func TestObserveNginxCommandOverride(t *testing.T) {
	needDocker(t)
	tmp := privateTemp(t)
	before := containerIDs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := []string{"nginx", "-g", "daemon off; worker_processes 1;"}
	res, err := observe.Observe(ctx, docker.Default(), observe.Options{
		Image:      "nginx:1.27-alpine",
		Pull:       true,
		Seconds:    30,
		Entrypoint: &[]string{},
		Cmd:        cmd,
		Log:        t.Logf,
	})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(res.Sysroot) })

	t.Logf("program %s, argv %v", res.Program, res.Argv)
	if !slices.Equal(res.Argv, cmd) {
		t.Errorf("Argv = %q, want %q", res.Argv, cmd)
	}
	if !strings.HasSuffix(res.Program, "nginx") {
		t.Errorf("Program = %q, want it to end with nginx", res.Program)
	}

	os.RemoveAll(res.Sysroot)
	checkNoLeftovers(t, before, tmp)
}

const entrypointDockerfile = `FROM alpine:3.20
RUN apk add --no-cache busybox-extras
COPY entry.sh /entry.sh
RUN chmod +x /entry.sh
RUN mkdir /www && echo hi > /www/index.html
EXPOSE 8080
ENTRYPOINT ["/entry.sh"]
`

const entryScript = `#!/bin/sh
echo starting
exec httpd -f -p 8080 -h /www
`

func TestObserveEntrypointScript(t *testing.T) {
	needDocker(t)
	c := docker.Default()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(entrypointDockerfile), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "entry.sh"), []byte(entryScript), 0755); err != nil {
		t.Fatal(err)
	}
	tag := "d2n-entrypoint-script-" + randomSuffix(t) + ":latest"
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), time.Minute)
		defer ccancel()
		_ = exec.CommandContext(cctx, "docker", "rmi", "--force", tag).Run()
	})
	if err := c.Build(ctx, docker.BuildOptions{Context: dir, Tag: tag, Quiet: true}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	tmp := privateTemp(t)
	before := containerIDs(t)
	res, err := observe.Observe(ctx, c, observe.Options{Image: tag, Seconds: 30, Log: t.Logf, KeepTrace: keepTrace(t, "entrypoint-script")})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	removed := false
	removeSysroot := func() {
		if !removed {
			removed = true
			os.RemoveAll(res.Sysroot)
		}
	}
	t.Cleanup(removeSysroot)

	t.Logf("program %s, argv %v, working dir %q", res.Program, res.Argv, res.WorkingDir)
	t.Logf("exposed ports %v, configuration files %v", res.ExposedPorts, res.ConfigurationFiles)
	for _, w := range res.Warnings {
		t.Logf("warning: %s", w)
	}
	files, total := list(t, res.Sysroot)
	logSysroot(t, files, total)

	if !strings.HasSuffix(res.Program, "httpd") && !strings.HasSuffix(res.Program, "busybox-extras") {
		t.Errorf("Program = %q, want httpd or busybox-extras", res.Program)
	}
	if _, ok := files[res.Program]; !ok {
		t.Errorf("the sysroot does not hold the program %s", res.Program)
	}
	if !slices.Contains(res.ExposedPorts, 8080) {
		t.Errorf("ExposedPorts = %v, want 8080 in it", res.ExposedPorts)
	}
	// the probe asked the port for /, and httpd opened the page
	if _, ok := files["/www/index.html"]; !ok {
		t.Errorf("the sysroot does not hold /www/index.html: the probe of / did not reach httpd")
	}
	// The code of TouchedPaths does not read what happened before the final
	// program started: the entrypoint script and the tools it ran are not
	// carried. The shell is only logged, since the loader may open it.
	if _, ok := files["/entry.sh"]; ok {
		t.Errorf("the sysroot holds /entry.sh: an entrypoint script is not carried")
	}
	_, hasSh := files["/bin/sh"]
	t.Logf("logged, not asserted: /bin/sh carried = %v", hasSh)

	removeSysroot()
	checkNoLeftovers(t, before, tmp)
}
