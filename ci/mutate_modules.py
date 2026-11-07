import subprocess, sys

# One mutation per CI job. M = {name: (old_text, new_text, test_regex, package)}.
# main() applies the mutation to exactly one of the source files of the package
# (the old text must occur exactly once in exactly one file), runs the tests that
# should catch it, and passes only when they FAIL (KILLED). SURVIVED means the
# tests still pass with the rule reverted; BROKEN means the mutation does not
# compile (a mistake in this script, not a result about the code).
#
# To keep the texts readable, "»" stands for a tab and "⏎" for a newline
# in the old and new texts below; A() turns them back.

TAB = "»"
NL = "⏎"

PKG = {
    "gen": ("internal/gen", ["gen.go", "command.go", "env.go", "names.go", "report.go"]),
    "interp": ("internal/interp", ["interp.go"]),
    "output": ("internal/output", ["output.go"]),
    "plan": ("internal/plan", ["plan.go"]),
    "observe": ("internal/observe", ["observe.go"]),
    "trace": ("internal/trace", ["trace.go"]),
    "docker": ("internal/docker", ["docker.go"]),
    "dockerfile": ("internal/dockerfile", ["parse.go", "dockerfile.go"]),
    "cmd": ("cmd", ["convert.go", "app.go"]),
}

M = {}


def A(name, pkg, tests, old, new):
    assert name not in M, name
    assert pkg in PKG, pkg
    M[name] = (old.replace(TAB, "\t").replace(NL, "\n"), new.replace(TAB, "\t").replace(NL, "\n"), tests, pkg)


# ---------------------------------------------------------------- internal/gen
A("gen-arch-default", "gen", "TestArchDefaultIsTheMachine",
  r'''case "amd64", "arm64":''', r'''case "amd64":''')
A("gen-order-deps", "gen", "TestOrderFollowsDependsOn",
  r'''if placed[s.Name] || !depsPlaced(s, placed) {''', r'''if placed[s.Name] {''')
A("gen-cycle-keeps-only-cycles", "gen", "TestOrderErrors",
  r'''if !needed && !dependsOnSelf(p.Service(name)) {''', r'''if false && !needed && !dependsOnSelf(p.Service(name)) {''')
A("gen-skip-profiles", "gen", "TestSkippedServices",
  r'''case len(s.Profiles) > 0:''', r'''case false:''')
A("gen-skip-zero-replicas", "gen", "TestSkippedServices",
  r'''case s.Replicas != nil && *s.Replicas == 0:''', r'''case false:''')
A("gen-folder-collision", "gen", "TestFolderNamesThatCollide",
  r'''if other, dup := folders[svc.PackageName]; dup {''', r'''if other, dup := folders[svc.PackageName]; dup && false {''')
A("gen-folder-dotdot", "gen", "TestNames",
  r'''for strings.Contains(name, "..") {''', r'''for false && strings.Contains(name, "..") {''')
A("gen-disable-args-copy", "gen", "TestProgramArgsAndPackage",
  r'''m.DisableArgsCopy = true''', r'''m.DisableArgsCopy = false''')
A("gen-program-item-redaction", "gen", "TestProgramArgsAndPackage|TestEnvNeverShowsValues",
  r'''msg += fmt.Sprintf(" and %d arguments, which are in package.manifest", n)''',
  r'''msg += fmt.Sprintf(" and %d arguments, which are in package.manifest", n) + " " + strings.Join(m.Args[1:], " ")''')
A("gen-env-service-wins", "gen", "TestEnvironment|TestServiceEnvIsOneSource",
  r'''for k, v := range fromService {⏎»»env[k] = v⏎»}''',
  r'''for k, v := range fromService {⏎»»if _, has := env[k]; !has {⏎»»»env[k] = v⏎»»}⏎»}''')
A("gen-env-environment-wins-over-file", "gen", "TestEnvironment|TestServiceEnvIsOneSource",
  r'''»»env[e.Name] = v⏎»}⏎»return env, items''',
  r'''»»if _, has := env[e.Name]; !has {⏎»»»env[e.Name] = v⏎»»}⏎»}⏎»return env, items''')
A("gen-env-note-valueless", "gen", "TestEnvironment",
  r'''add(plan.Note, key, "the variable has no value in the file and is not set on this machine; it is not set in the package")''', r'''_ = key''')
A("gen-ports-default-tcp", "gen", "TestPorts",
  r'''»»if proto == "" {⏎»»»proto = "tcp"⏎»»}⏎''', r'''''')
A("gen-ports-not-published", "gen", "TestPorts",
  r'''if p.Published == "" {''', r'''if false {''')
A("gen-ports-range", "gen", "TestPorts",
  r'''if strings.Contains(p.Published, "-") || strings.Contains(p.Target, "-") {''', r'''if false {''')
A("gen-ports-host-ip", "gen", "TestPortsTwoKindsAtOnce",
  r'''if p.HostIP != "" {''', r'''if false {''')
A("gen-ports-remap", "gen", "TestPorts|TestPortsTwoKindsAtOnce",
  r'''if p.Published != p.Target {''', r'''if false {''')
A("gen-ports-udp", "gen", "TestPorts",
  r'''if proto == "udp" {''', r'''if false {''')
A("gen-volume-bind", "gen", "TestVolumes",
  r'''case v.Type == "bind":''', r'''case false:''')
A("gen-volume-named", "gen", "TestVolumes",
  r'''case v.Type == "volume":''', r'''case false:''')
A("gen-volume-anonymous", "gen", "TestVolumes",
  r'''case v.Type == "volume" && v.Source == "":''', r'''case false:''')
A("gen-restart-no", "gen", "TestRestart",
  r'''case r == "" || r == "no":''', r'''case r == "":''')
A("gen-restart-reboot", "gen", "TestRestart",
  r'''m.RebootOnExit = true''', r'''m.RebootOnExit = false''')
A("gen-depends-condition", "gen", "TestDependsOnConditions",
  r'''if d.Condition != "" && d.Condition != "service_started" {''', r'''if d.Condition != "" {''')
A("gen-healthcheck-disable", "gen", "TestHealthcheck",
  r'''case h != nil && h.Disable:''', r'''case false:''')
A("gen-healthcheck-dockerfile", "gen", "TestHealthcheck",
  r'''case si.Stage != nil && si.Stage.Healthcheck != nil && !si.Stage.Healthcheck.None:''', r'''case false:''')
A("gen-healthcheck-names-only", "gen", "TestHealthcheck|TestEnvNeverShowsValues",
  r'''return fmt.Sprintf("the test starts with %s and has %d words (it is in the file)", w[0], len(w))''', r'''return strings.Join(w, " ")''')
A("gen-networks-default", "gen", "TestNetworks",
  r'''if n != "default" {''', r'''if true {''')
A("gen-user-root-zero", "gen", "TestUserAndWorkingDir",
  r'''u != "root" && u != "0" {''', r'''u != "root" {''')
A("gen-workdir-observed", "gen", "TestUserAndWorkingDir",
  r'''case obs.WorkingDir != "" && obs.WorkingDir != "/":''', r'''case false:''')
A("gen-workdir-file-only", "gen", "TestUserAndWorkingDir",
  r'''case s.WorkingDir != "" && s.WorkingDir != obs.WorkingDir:''', r'''case false:''')
# names.go
A("names-clean-lowercase", "gen", "TestPackageNames|TestNames",
  r'''for _, r := range strings.ToLower(s) {''', r'''for _, r := range s {''')
A("names-shorten-hash-length", "gen", "TestPackageNames|TestLongNamesStayApart",
  r'''hex.EncodeToString(sum[:])[:6]''', r'''hex.EncodeToString(sum[:])[:5]''')
A("names-dns-reserved", "gen", "TestPackageNames",
  r'''if pkg == reservedPkg {''', r'''if false {''')
A("names-collision", "gen", "TestPackageNames|TestNames",
  r'''if other, dup := owner[pkg]; dup {''', r'''if other, dup := owner[pkg]; dup && false {''')
# report.go
A("report-kind-order", "gen", "TestRender",
  r'''plan.Unsupported, plan.Approximated, plan.Note, plan.Observed, plan.Converted}''', r'''plan.Unsupported, plan.Note, plan.Approximated, plan.Observed, plan.Converted}''')
# command.go
A("command-entrypoint-clears-cmd", "gen", "TestEffectiveCommand",
  r'''»case s.Entrypoint.IsSet():⏎»case img != nil:⏎»»cmd = append(cmd, img.Cmd...)''', r'''»case img != nil:⏎»»cmd = append(cmd, img.Cmd...)''')
A("command-empty-entrypoint-is-none", "gen", "TestEffectiveCommand",
  r'''if len(ep) > 0 {⏎»»entrypoint = &ep⏎»}''', r'''entrypoint = &ep''')

# ------------------------------------------------------------- internal/interp
A("interp-double-dollar", "interp", "TestExpand",
  r'''case n == '$':''', r'''case false:''')
A("interp-default-with-colon", "interp", "TestExpand",
  r'''case strings.HasPrefix(rest, ":-"):⏎»»if !ok || v == "" {''', r'''case strings.HasPrefix(rest, ":-"):⏎»»if !ok {''')
A("interp-default-without-colon", "interp", "TestExpand",
  r'''case strings.HasPrefix(rest, "-"):⏎»»if !ok {''', r'''case strings.HasPrefix(rest, "-"):⏎»»if !ok || v == "" {''')
A("interp-replacement-with-colon", "interp", "TestExpand",
  r'''case strings.HasPrefix(rest, ":+"):⏎»»if ok && v != "" {''', r'''case strings.HasPrefix(rest, ":+"):⏎»»if ok {''')
A("interp-replacement-without-colon", "interp", "TestExpand",
  r'''case strings.HasPrefix(rest, "+"):⏎»»if ok {''', r'''case strings.HasPrefix(rest, "+"):⏎»»if ok && v != "" {''')
A("interp-required-with-colon", "interp", "TestExpand|TestExpandErrors",
  r'''case strings.HasPrefix(rest, ":?"):⏎»»if !ok || v == "" {''', r'''case strings.HasPrefix(rest, ":?"):⏎»»if !ok {''')
A("interp-required-without-colon", "interp", "TestExpand|TestExpandErrors",
  r'''case strings.HasPrefix(rest, "?"):⏎»»if !ok {''', r'''case strings.HasPrefix(rest, "?"):⏎»»if !ok || v == "" {''')
A("interp-nested-braces", "interp", "TestExpand",
  r'''depth++''', r'''depth += 0''')
A("interp-unclosed-brace", "interp", "TestExpandErrors",
  r'''return 0, errors.New("a ${ is not closed")''', r'''return len(s), nil''')
A("envfile-export", "interp", "TestParseEnvFile",
  r'''line = strings.TrimLeft(rest, " \t")''', r'''_ = rest''')
A("envfile-inline-comment", "interp", "TestParseEnvFile",
  r'''if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {''', r'''if v[i] == '#' {''')
A("envfile-single-quotes-literal", "interp", "TestParseEnvFile",
  r'''»»return body, used, nil''', r'''»»out, err := st.expand(body)⏎»»return out, used, err''')
A("envfile-double-quote-escapes", "interp", "TestParseEnvFile",
  r'''out, err := st.expand(unescape(body))''', r'''out, err := st.expand(body)''')
A("envfile-multiline-value", "interp", "TestParseEnvFile",
  r'''b.WriteByte('\n')⏎»»cur = more[used]''', r'''b.WriteByte(' ')⏎»»cur = more[used]''')

# --------------------------------------------------------------- internal/plan
A("plan-reboot-on-exit-always", "plan", "TestManifestJSONAlwaysWritesWhatOpsKeepsBetweenPackages|TestManifestJSONFull",
  r'''RebootOnExit bool `json:"RebootOnExit"`''', r'''RebootOnExit bool `json:",omitempty"`''')
A("plan-ports-empty-list", "plan", "TestManifestJSONAlwaysWritesWhatOpsKeepsBetweenPackages",
  r'''»if out.Ports == nil {⏎»»out.Ports = []string{}⏎»}⏎''', r'''''')
A("plan-udp-ports-empty-list", "plan", "TestManifestJSONAlwaysWritesWhatOpsKeepsBetweenPackages",
  r'''»if out.UDPPorts == nil {⏎»»out.UDPPorts = []string{}⏎»}⏎''', r'''''')

# ------------------------------------------------------------- internal/output
A("output-ops-home-is-the-parent", "output", "TestWriteExactFiles|TestWriteOpsHomeFromEnvironment",
  r'''base := filepath.Join(home, ".ops", "local_packages", p.Arch)''', r'''base := filepath.Join(home, "local_packages", p.Arch)''')
A("output-manifest-with-env-is-0600", "output", "TestWriteExactFiles",
  r'''mode = 0o600''', r'''mode = 0o644''')
A("output-manifest-without-env-is-0644", "output", "TestWriteExactFiles",
  r'''mode := fs.FileMode(0o644)''', r'''mode := fs.FileMode(0o600)''')
A("output-rollback-order", "output", "TestWriteRollbackWhenSecondPackageFails|TestWriteRollbackKeepsWhatExisted|TestWriteCrossDeviceKeepsSourceOnFailure",
  r'''for i := len(j.undo) - 1; i >= 0; i-- {''', r'''for i := 0; i < len(j.undo); i++ {''')
A("output-finish-runs", "output", "TestWriteForceReplaces|TestWriteCrossDeviceRemovesSourceAfterSuccess",
  r'''»»»j.finish()''', r'''»»»_ = j''')
A("output-mkdir-rolled-back", "output", "TestWriteRollbackWhenSecondPackageFails",
  r'''j.onFail(func() { os.RemoveAll(top) })''', r'''_ = top''')
A("output-force-replaces-package", "output", "TestWriteForceReplaces|TestWriteRollbackKeepsWhatExisted",
  r'''if exists(final) {⏎»»if !force {''', r'''if false {⏎»»if !force {''')
A("output-exdev-only", "output", "TestMoveTreeOtherErrorsDoNotCopy",
  r'''if !errors.Is(err, syscall.EXDEV) {''', r'''if !errors.Is(err, syscall.EXDEV) && false {''')
A("output-exdev-removes-source", "output", "TestWriteCrossDeviceRemovesSourceAfterSuccess",
  r'''j.onOK(func() { os.RemoveAll(src) })''', r'''_ = src''')
A("output-copy-keeps-modes", "output", "TestCopyTreeKeepsModesAndLinks",
  r'''return m & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)''', r'''return 0o755''')
A("output-copy-keeps-links", "output", "TestCopyTreeKeepsModesAndLinks|TestMoveTreeFallsBackToCopyOnCrossDevice",
  r'''case info.Mode()&fs.ModeSymlink != 0:''', r'''case false:''')
A("output-name-dotdot", "output", "TestWriteRefusesBadNames",
  r'''strings.Contains(s, "..")''', r'''s == ".."''')
A("output-ops-home-env", "output", "TestWriteOpsHomeFromEnvironment",
  r'''h = os.Getenv("OPS_HOME")''', r'''h = ""''')

# ------------------------------------------------------------ internal/observe
A("observe-entrypoint-drops-image-cmd", "observe", "TestObserveCommandOverrides|TestObserveEmptyEntrypointWithoutCmdStartsNothing",
  r'''entrypoint = *o.Entrypoint⏎»»cmd = nil''', r'''entrypoint = *o.Entrypoint''')
A("observe-args-last", "observe", "TestObserveCommandOverrides|TestObserveTargetExecutableAndArgs",
  r'''return append(argv, o.Args...)''', r'''return argv''')
A("observe-user-root-zero", "observe", "TestObserveUserOfTheImageRunsTheProgram",
  r'''if name == "" || name == "root" || name == "0" {''', r'''if name == "" || name == "root" {''')
A("observe-user-group-cut", "observe", "TestObserveUserOfTheImageRunsTheProgram",
  r'''name, _, _ := strings.Cut(imageUser, ":")''', r'''name := imageUser''')
A("observe-container-starts-as-root", "observe", "TestObserveSuccess|TestObserveUserOfTheImageRunsTheProgram",
  r'''User:        "0",''', r'''User:        cfg.User,''')
A("observe-tracer-out-read-only", "observe", "TestObserveSuccess|TestObserveBindsFollowTheTracerBinds",
  r'''trace.TraceMount + "/out:ro"}''', r'''trace.TraceMount + "/out"}''')
A("observe-bind-under-tracer", "observe", "TestObserveBindUnderTheTracerIsAnError",
  r'''if t == trace.TraceMount || strings.HasPrefix(t, trace.TraceMount+"/") {''', r'''if t == trace.TraceMount {''')
A("observe-bind-option", "observe", "TestObserveBindUnderTheTracerIsAnError",
  r'''if len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw" {''', r'''if false {''')
A("observe-pull-off", "observe", "TestObserveImageMissingWithoutPull",
  r'''if !r.o.Pull {''', r'''if false {''')
A("observe-stop-before-remove", "observe", "TestObserveSuccess|TestObserveStartFailureRemovesBoth",
  r'''defer r.discard(id, true)''', r'''defer r.discard(id, false)''')
A("observe-cleanup-warning", "observe", "TestObserveCleanupErrorIsAWarning",
  r'''res.Warnings = append(res.Warnings, "cleanup: "+c.Error())''', r'''_ = c''')
A("observe-tracer-dir-removed", "observe", "TestObserveSuccess",
  r'''defer os.RemoveAll(tracerDir)''', r'''_ = tracerDir''')
A("observe-env-values-redacted", "observe", "TestObserveSecretsStayOutOfErrorsAndLogs",
  r'''text = strings.ReplaceAll(text, v, "***")''', r'''_ = v''')
A("observe-lines-before-the-program", "observe", "TestObserveSuccess",
  r'''trace.TouchedPaths(raw, cwd, from)''', r'''trace.TouchedPaths(raw, cwd, 0)''')
A("observe-console-link-empty-file", "observe", "TestObserveSuccess",
  r'''case trace.IsConsoleLink(link):''', r'''case false:''')
A("observe-half-written-file-removed", "observe", "TestObserveHalfWrittenFileIsRemoved",
  r'''// half of a file is not a file⏎»»»os.Remove(target)''', r'''// half of a file is not a file''')

# -------------------------------------------------------------- internal/trace
A("trace-wrap-user", "trace", "TestWrapCommand",
  r'''if user != "" {''', r'''if false {''')
A("trace-lines-before-from", "trace", "TestTouchedPaths|TestTouchedPathsFromLineZero",
  r'''collect := i >= from''', r'''collect := i >= from || i >= 0''')
A("trace-chdir-sets-cwd", "trace", "TestTouchedPathsChdir",
  r'''»»»»cwd[pid] = name⏎»»»}''', r'''»»»»_ = name⏎»»»}''')
A("trace-fchdir-makes-cwd-unknown", "trace", "TestTouchedPathsChdir|TestTouchedPathsFromLineZero",
  r'''} else if result, found := ResultOf(args); found && result == "0" {⏎»»»»cwd[pid] = ""''', r'''} else if result, found := ResultOf(args); found && result == "0" {⏎»»»»_ = pid''')
A("trace-child-inherits-cwd", "trace", "TestTouchedPathsChdir",
  r'''cwd[child[3]] = cwd[pid]''', r'''cwd[child[3]] = workingDir''')
A("trace-unparented-process-cwd", "trace", "TestTouchedPathsChdir",
  r'''cwd[pid] = cwd[active]''', r'''cwd[pid] = workingDir''')
A("trace-superseded-hands-cwd-over", "trace", "TestTouchedPathsSuperseded",
  r'''»»»»cwd[to] = cwd[pid]⏎''', r'''''')
A("trace-unfinished-call-is-taken", "trace", "TestTouchedPaths",
  r'''unfinished := strings.Contains(after, "<unfinished ...>")''', r'''unfinished := false''')
A("trace-created-leaves-out-not-carried", "trace", "TestCreatedPathsLeavesOutWhatIsNeverCarried",
  r'''if !NotCarried(p) {⏎»»»out = append(out, p)⏎»»}''', r'''out = append(out, p)''')
A("trace-created-trunc-needs-write", "trace", "TestCreatedPaths",
  r'''slices.Contains(flags, "O_TRUNC") && (slices.Contains(flags, "O_WRONLY") || slices.Contains(flags, "O_RDWR"))''', r'''slices.Contains(flags, "O_TRUNC")''')
A("trace-final-exec-parentage", "trace", "TestFinalProgram|TestFinalExec",
  r'''parent[m[3]] = m[1] + m[2]''', r'''_ = m''')
A("trace-final-exec-exited-process", "trace", "TestFinalProgram",
  r'''gone[m[1]+m[2]] = true''', r'''_ = m''')
A("trace-final-exec-failed-resume-cancels", "trace", "TestFinalProgram",
  r'''if state.hadPrevious {⏎»»»»»last[process] = state.previous''', r'''if false {⏎»»»»»last[process] = state.previous''')
A("trace-not-carried-prefix-boundary", "trace", "TestNotCarried",
  r'''strings.HasPrefix(p, prefix+"/")''', r'''strings.HasPrefix(p, prefix)''')

# ------------------------------------------------------------- internal/docker
A("docker-build-args-sorted", "docker", "TestBuildArgs",
  r'''sort.Strings(keys)⏎»for _, k := range keys {⏎»»args = append(args, "--build-arg"''', r'''for _, k := range keys {⏎»»args = append(args, "--build-arg"''')
A("docker-build-context-after-dashes", "docker", "TestBuildArgs",
  r'''return append(args, "--", o.Context)''', r'''return append(args, o.Context)''')
A("docker-option-like-argument", "docker", "TestOptionLikeArgumentsAreRefused",
  r'''if v[0] == '-' {''', r'''if false {''')
A("docker-redact-short-env-flag", "docker", "TestExitErrorRedaction|TestExecRunnerSecretsAreRedacted",
  r'''args[i-1] == "--env" || args[i-1] == "-e" ||''', r'''args[i-1] == "--env" ||''')
A("docker-redact-env-equals", "docker", "TestExitErrorRedaction",
  r'''case strings.HasPrefix(a, "--env="):''', r'''case false:''')
A("docker-redact-value", "docker", "TestExitErrorRedaction|TestExecRunnerSecretsAreRedacted",
  r'''return v[:i+1] + "***"''', r'''return v''')
A("docker-stderr-tail-whole-runes", "docker", "TestTailOfKeepsRunes",
  r'''for i < len(s) && !utf8.RuneStart(s[i]) {''', r'''for false && i < len(s) && !utf8.RuneStart(s[i]) {''')
A("docker-kill-after-wait-delay", "docker", "TestExecRunnerCancelEscalatesToKill",
  r'''const waitDelay = 2 * time.Second''', r'''const waitDelay = 60 * time.Second''')
A("docker-cancel-reports-context", "docker", "TestExecRunnerCancelWhileRunning|TestExecRunnerSecretsAreRedacted",
  r'''if ctx.Err() != nil {⏎»»return errBuf.Bytes(), fmt.Errorf("%s: %w", line, ctx.Err())''', r'''if false {⏎»»return errBuf.Bytes(), fmt.Errorf("%s: %w", line, ctx.Err())''')
A("docker-write-error-beats-exit-error", "docker", "TestStreamWriteErrorBeatsExitError",
  r'''if ew.err != nil {''', r'''if false {''')
A("docker-has-image-daemon-down", "docker", "TestHasImage",
  r'''return false, wrap("docker image inspect", err, stderr)''', r'''return false, nil''')
A("docker-create-empty-entrypoint", "docker", "TestCreateArgsEntrypoint",
  r'''if len(ep) == 0 {⏎»»»args = append(args, "--entrypoint", "")''', r'''if false {⏎»»»args = append(args, "--entrypoint", "")''')
A("docker-exec-not-running", "docker", "TestExecDaemonFailures",
  r'''strings.Contains(low, "container") && strings.Contains(low, "is not running")''', r'''strings.Contains(low, "is not running")''')
A("docker-cp-relative-colon", "docker", "TestSimpleCommands",
  r'''(hostDir[0] == '-' || strings.Contains(hostDir, ":"))''', r'''(hostDir[0] == '-')''')

# ---------------------------------------------------------- internal/dockerfile
A("dockerfile-from-platform", "dockerfile", "TestFromForms|TestFlagNames",
  r'''if o.name != "platform" {''', r'''if true {''')
A("dockerfile-escape-directive", "dockerfile", "TestEscapeDirective|TestDirectives",
  r'''p.esc = m[2][0]''', r'''_ = p''')
A("dockerfile-json-or-shell-fallback", "dockerfile", "TestJSONLikeShellForm",
  r'''if kind == brokenList && !strict {''', r'''if false {''')
A("dockerfile-run-json-is-strict", "dockerfile", "TestParseErrors",
  r'''if kind == brokenList && !strict {''', r'''if kind == brokenList {''')
A("dockerfile-json-only-strings", "dockerfile", "TestParseErrors",
  r'''if len(e) == 0 || e[0] != '"' || json.Unmarshal(e, &str) != nil {''', r'''if false {''')
A("dockerfile-empty-exec-list-is-set", "dockerfile", "TestCommandForms",
  r'''return Command{Exec: list, Set: true}, nil''', r'''return Command{Exec: list, Set: len(list) > 0}, nil''')
A("dockerfile-healthcheck-none", "dockerfile", "TestStageFields",
  r'''h.None = true''', r'''_ = h''')
A("dockerfile-copy-from-chown-chmod", "dockerfile", "TestStageFields",
  r'''case "from", "chown", "chmod":''', r'''case "chown", "chmod":''')
A("dockerfile-heredoc-quotes-match", "dockerfile", "TestHeredocs",
  r'''if m != nil && m[2] == m[4] {''', r'''if m != nil {''')
A("dockerfile-expose-forms", "dockerfile", "TestExpose",
  r'''case w.ref || exposeRe.MatchString(w.val):''', r'''case true:''')
A("dockerfile-variable-reference", "dockerfile", "TestVariableNotes",
  r'''default:⏎»»»begin(i)⏎»»»if c == '$' {⏎»»»»ref = true''', r'''default:⏎»»»begin(i)⏎»»»if false {⏎»»»»ref = true''')
A("dockerfile-unknown-in-stage", "dockerfile", "TestUnknown",
  r'''if p.cur != nil {⏎»»p.cur.Unknown = append(p.cur.Unknown, msg)''', r'''if false {⏎»»p.cur.Unknown = append(p.cur.Unknown, msg)''')
A("dockerfile-base-stage-case", "dockerfile", "TestBaseStage",
  r'''strings.EqualFold(prev.Name, s.Base)''', r'''prev.Name == s.Base''')
A("dockerfile-base-stage-earlier-only", "dockerfile", "TestBaseStage",
  r'''for i := 0; i < idx; i++ {''', r'''for i := 0; i < len(f.Stages) && idx >= 0; i++ {''')

# ------------------------------------------------------------------------- cmd
A("cmd-image-tag-separators-collapse", "cmd", "TestImageTag",
  r'''if sep && b.Len() > 0 {''', r'''if sep && b.Len() > 0 && false {''')
A("cmd-image-tag-ends-alphanumeric", "cmd", "TestImageTag",
  r'''out := strings.Trim(b.String(), "-_.")''', r'''out := b.String()''')
A("cmd-image-tag-empty-component", "cmd", "TestImageTag",
  r'''if out == "" {⏎»»return "x"''', r'''if false {⏎»»return "x"''')
A("cmd-arch-different", "cmd", "TestArch",
  r'''} else if si.Image.Architecture != arch {''', r'''} else if false {''')
A("cmd-skip-profiles", "cmd", "TestSkippedServices",
  r'''case len(s.Profiles) > 0:''', r'''case false:''')
A("cmd-skip-not-selected", "cmd", "TestServiceFilter",
  r'''case only != nil && !only[s.Name]:''', r'''case false:''')
A("cmd-package-names-checked-early", "cmd", "TestPackageNamesChecked",
  r'''gen.PackageNames(project, converted); err != nil {''', r'''gen.PackageNames(project, converted); err != nil && false {''')
A("cmd-report-before-generate-error", "cmd", "TestGenerateErrorPrintsReportThenError",
  r'''if p != nil {''', r'''if false {''')
A("cmd-report-before-write-error", "cmd", "TestWriteErrorPrintsReportThenError",
  r'''Force: o.force})⏎»if err != nil {⏎»»fmt.Fprint(a.Stdout, gen.Render(p.Report))⏎''', r'''Force: o.force})⏎»if err != nil {⏎''')
A("cmd-strict-exit-status", "cmd", "TestStrict",
  r'''return 2, nil''', r'''return 1, nil''')
A("cmd-sysroots-removed", "cmd", "TestSysrootsRemovedAfterWrite|TestObserveErrorStopsTheRun",
  r'''a.RemoveAll(s)''', r'''_ = s''')
A("cmd-pull-only-with-flag", "cmd", "TestPullRules",
  r'''if !o.pull {''', r'''if false {''')
A("cmd-entrypoint-always-passed", "cmd", "TestObserveCommand",
  r'''if ep == nil {⏎»»ep = &[]string{}''', r'''if false {⏎»»ep = &[]string{}''')
A("cmd-build-arg-unset-is-left-out", "cmd", "TestBuildCall|TestBuildArgUnresolved",
  r'''if !ok {⏎»»»»continue''', r'''if !ok && false {⏎»»»»continue''')

# ----------------------------------------------- mutations for the coverage tests
# gen
A("gen-ports-variable-in-target", "gen", "TestPorts",
  r'''if strings.Contains(p.Published, "$") || strings.Contains(p.Target, "$") {''', r'''if strings.Contains(p.Published, "$") {''')
A("gen-ports-range-in-target", "gen", "TestPorts",
  r'''if strings.Contains(p.Published, "-") || strings.Contains(p.Target, "-") {''', r'''if strings.Contains(p.Published, "-") {''')
A("gen-volume-tmpfs", "gen", "TestVolumes",
  r'''case v.Type == "tmpfs":''', r'''case false:''')
A("gen-depends-empty-condition", "gen", "TestDependsOnEmptyConditionIsSilent",
  r'''if d.Condition != "" && d.Condition != "service_started" {''', r'''if d.Condition != "service_started" {''')
A("gen-healthcheck-dockerfile-none", "gen", "TestDockerfileHealthcheckNoneGivesNoItem",
  r'''si.Stage.Healthcheck != nil && !si.Stage.Healthcheck.None:''', r'''si.Stage.Healthcheck != nil:''')
A("gen-testsummary-cmd-shell", "gen", "TestTestSummary",
  r'''} else if len(w) > 0 && w[0] == "CMD-SHELL" {''', r'''} else if false && len(w) > 0 && w[0] == "CMD-SHELL" {''')
A("gen-testsummary-empty-text", "gen", "TestEmptyHealthcheckTestMessage|TestTestSummary",
  r'''return "the test is empty"''', r'''return "the test is blank"''')
A("gen-project-name-fallback", "gen", "TestProjectNameFallback",
  r'''return "project"''', r'''return "proj"''')
A("gen-folder-dotdot-every-run", "gen", "TestFolderNameCollapsesEveryRun",
  r'''for strings.Contains(name, "..") {''', r'''if strings.Contains(name, "..") {''')
A("gen-env-nil-lookup", "gen", "TestServiceEnvNilFunctions",
  r'''if lookup == nil {''', r'''if false && lookup == nil {''')
A("gen-env-nil-readfile", "gen", "TestServiceEnvNilFunctions",
  r'''if readFile == nil {''', r'''if false && readFile == nil {''')
A("gen-env-file-unset-note-text", "gen", "TestServiceEnvFileUnsetNote",
  r'''"variables used in the file are not set on this machine and became empty: "''', r'''"variables used in the file are not set on this machine and became blank: "''')
A("gen-env-file-unset-note-guard", "gen", "TestServiceEnvFileUnsetNote",
  r'''if len(unset) > 0 {⏎»»»add(plan.Note, key, "variables used in the file''', r'''if len(unset) > 0 && false {⏎»»»add(plan.Note, key, "variables used in the file''')
A("names-length-boundary", "gen", "TestPackageNameLengthBoundary",
  r'''if len(pkg) > maxPkgLen {''', r'''if len(pkg) >= maxPkgLen {''')
A("names-hash-of-original-name", "gen", "TestPackageNameHashIsOfTheOriginalName",
  r'''sha1.Sum([]byte(service))''', r'''sha1.Sum([]byte(pkg))''')
A("report-converted-last", "gen", "TestRenderPutsConvertedLast",
  r'''[]plan.Kind{plan.Unsupported, plan.Approximated, plan.Note, plan.Observed, plan.Converted}''', r'''[]plan.Kind{plan.Converted, plan.Unsupported, plan.Approximated, plan.Note, plan.Observed}''')
A("report-max-listed", "gen", "TestListedLimit",
  r'''const maxListed = 20''', r'''const maxListed = 19''')
A("report-listed-at-the-limit", "gen", "TestListedLimit",
  r'''if len(names) <= maxListed {''', r'''if len(names) < maxListed {''')
# interp
A("interp-digits-in-names", "interp", "TestExpandDigitsInNames",
  "return isNameStart(c) || c >= '0' && c <= '9'", "return isNameStart(c)")
A("interp-double-dollar-in-braces", "interp", "TestExpandDoubleDollarInsideBraces",
  r'''case s[i] == '$' && i+1 < len(s) && s[i+1] == '$':⏎»»»i++''', r'''case false:⏎»»»i++''')
A("interp-invalid-body-empty", "interp", "TestExpandInvalidBodyEmpty",
  r'''if body == "" || !isNameStart(body[0]) {''', r'''if !isNameStart(body[0]) {''')
A("interp-invalid-body-digit", "interp", "TestExpandInvalidBodyStartsWithDigit",
  r'''if body == "" || !isNameStart(body[0]) {''', r'''if body == "" {''')
# output
A("output-precheck-compose-exists", "output", "TestWritePreCheckComposeExistsTouchesNothing",
  r'''if exists(composePath) {''', r'''if false && exists(composePath) {''')
A("output-precheck-package-exists", "output", "TestWritePreCheckPackageFolderExistsTouchesNothing",
  r'''if exists(final) {⏎»»»»return nil, fmt.Errorf("output: package folder''', r'''if false && exists(final) {⏎»»»»return nil, fmt.Errorf("output: package folder''')
A("output-duplicate-package-name", "output", "TestWriteDuplicatePackageNameMessage",
  r'''if seen[s.PackageName] {''', r'''if false && seen[s.PackageName] {''')
A("output-sysroot-empty", "output", "TestWriteSysrootErrors",
  r'''if s.Sysroot == "" {''', r'''if false && s.Sysroot == "" {''')
A("output-sysroot-not-a-directory", "output", "TestWriteSysrootErrors",
  r'''if !fi.IsDir() {⏎»»return "", fmt.Errorf("output: sysroot %s''', r'''if false && !fi.IsDir() {⏎»»return "", fmt.Errorf("output: sysroot %s''')
A("output-sysroot-missing", "output", "TestWriteSysrootErrors",
  r'''fi, err := os.Lstat(s.Sysroot)⏎»if err != nil {⏎»»return "", fmt.Errorf("output: sysroot of ''', r'''fi, err := os.Lstat(s.Sysroot)⏎»if err != nil {⏎»»return "", fmt.Errorf("output: sysroot for ''')
A("output-package-folder-mode", "output", "TestWriteModes",
  r'''os.Chmod(tmp, 0o755)''', r'''os.Chmod(tmp, 0o700)''')
A("output-compose-mode", "output", "TestWriteModes",
  r'''os.Chmod(tmp, 0o644)''', r'''os.Chmod(tmp, 0o600)''')
A("output-failed-copy-removed", "output", "TestMoveTreeFailedCopyLeavesNothing",
  r'''os.RemoveAll(dst)''', r'''_ = dst''')
A("output-keep-setuid", "output", "TestCopyTreeKeepsSpecialBits",
  r'''fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)''', r'''fs.ModePerm | fs.ModeSetgid | fs.ModeSticky)''')
A("output-keep-setgid", "output", "TestCopyTreeKeepsSpecialBits",
  r'''fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)''', r'''fs.ModePerm | fs.ModeSetuid | fs.ModeSticky)''')
A("output-keep-sticky", "output", "TestCopyTreeKeepsSpecialBits",
  r'''fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)''', r'''fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid)''')
# observe
A("observe-program-found-on-path", "observe", "TestObserveProgramWithoutASlashIsFoundOnThePath",
  r'''if !strings.HasPrefix(program, "/") {''', r'''if false && !strings.HasPrefix(program, "/") {''')
A("observe-program-not-opened", "observe", "TestObserveProgramNotOpenedIsAnError",
  r'''if _, err := os.Stat(filepath.Join(sysroot, filepath.FromSlash(program))); err != nil {''', r'''if _, err := os.Stat(filepath.Join(sysroot, filepath.FromSlash(program))); err != nil && false {''')
A("observe-ports-sorted", "observe", "TestObserveProbesInTheOrderOfThePorts",
  r'''sort.Ints(ports)''', r'''sort.Ints(nil)''')
A("observe-prober-other-host", "observe", "TestHTTPProberDoesNotFollowARedirectToAnotherHost",
  r'''req.URL.Hostname() != via[0].URL.Hostname() ||''', r'''false ||''')
A("observe-prober-ten-requests", "observe", "TestHTTPProberStopsAtTenRequests",
  r'''len(via) >= 10''', r'''len(via) >= 11''')
# trace
A("trace-openat2-flags-position", "trace", "TestCreatedPathsOpenat2FlagPositions",
  r'''s = s[at+len("flags="):]''', r'''s = s[at+len("flags=")-1:]''')
A("trace-insysroot-forty-links", "trace", "TestInSysrootFollowsFortyLinks",
  r'''if links > 40 {''', r'''if links >= 40 {''')
A("trace-insysroot-forty-first-link", "trace", "TestInSysrootStopsAtTheFortyFirstLink",
  r'''if links > 40 {''', r'''if links > 41 {''')
# docker
A("docker-tail-writer-keeps-whole-write", "docker", "TestTailWriterExactMax",
  r'''t.buf = append(t.buf[:0], p[len(p)-t.max:]...)''', r'''t.buf = append(t.buf[:0], p...)''')
A("docker-ip-address-network-order", "docker", "TestIPAddressNetworkOrder",
  r'''sort.Strings(names)''', r'''sort.Strings(nil)''')
# cmd
A("cmd-remote-context-url", "cmd", "TestRemoteBuildContextIsRefused",
  r'''strings.Contains(ctxDir, "://") ||''', r'''false ||''')
A("cmd-remote-context-git", "cmd", "TestRemoteBuildContextIsRefused",
  r'''|| strings.HasPrefix(ctxDir, "git@")''', r'''''')
A("cmd-binds-only-bind-type", "cmd", "TestOnlyBindVolumesWithASourceReachTheObservation",
  r'''v.Type != "bind" || v.Source == ""''', r'''v.Source == ""''')
A("cmd-binds-need-a-source", "cmd", "TestOnlyBindVolumesWithASourceReachTheObservation",
  r'''v.Type != "bind" || v.Source == "" ||''', r'''v.Type != "bind" ||''')
A("cmd-depends-on-missing-service", "cmd", "TestDependsOnMissingServiceIsAnError",
  r'''if p.Service(d.Service) == nil {''', r'''if false && p.Service(d.Service) == nil {''')
A("cmd-sysroot-of-failed-observation", "cmd", "TestSysrootOfAnObservationThatFailedIsRemoved",
  r'''if si.Observed != nil && si.Observed.Sysroot != "" {''', r'''if err == nil && si.Observed != nil && si.Observed.Sysroot != "" {''')
A("cmd-dockerfile-default-name", "cmd", "TestDockerfileDefaultNameIsAppWhenTheFolderCleansToNothing",
  r'''name = "app"''', r'''name = "x"''')
A("cmd-empty-output-flag", "cmd", "TestEmptyOutputFlagIsAnError",
  r'''if o.out == "" {''', r'''if false && o.out == "" {''')


def main():
    if sys.argv[1] == "list":
        print(" ".join(M))
        return
    name = sys.argv[1]
    old, new, tests, pkg = M[name]
    directory, names = PKG[pkg]
    files = ["%s/%s" % (directory, n) for n in names]
    hit = 0
    for f in files:
        s = open(f).read()
        if s.count(old) == 1:
            open(f, "w").write(s.replace(old, new))
            hit += 1
        elif s.count(old) > 1:
            sys.exit("%s: pattern found more than once in %s" % (name, f))
    if hit != 1:
        sys.exit("%s: pattern found in %d files" % (name, hit))
    r = subprocess.run(["go", "test", "-count=1", "-timeout", "120s", "-run", "^(" + tests + ")$", "./" + directory],
                       capture_output=True, text=True)
    out = r.stdout + r.stderr
    print(out[-3000:])
    if r.returncode == 0:
        sys.exit("SURVIVED: %s: the tests still pass with the change reverted" % name)
    if "[build failed]" in out or "setup failed" in out:
        sys.exit("BROKEN MUTATION: %s does not compile" % name)
    print("KILLED: %s" % name)


if __name__ == "__main__":
    main()
