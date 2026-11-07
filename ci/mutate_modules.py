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
    "observe": ("internal/observe", ["observe.go", "start.go"]),
    "trace": ("internal/trace", ["trace.go", "children.go"]),
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
A("gen-workdir-observed", "gen", "TestUserAndWorkingDir|TestWorkingDirIsInTheManifest|TestEveryServiceHasACwd",
  r'''m.ManifestPassthrough = map[string]any{"cwd": cwd}''', r'''m.ManifestPassthrough = map[string]any{"cwd": "/"}''')
A("gen-workdir-file-only", "gen", "TestUserAndWorkingDir|TestWorkingDirIsInTheManifest",
  r'''case s.WorkingDir != "" && path.Clean(s.WorkingDir) != path.Clean(cwd):''', r'''case s.WorkingDir != "" && s.WorkingDir != path.Clean(cwd):''')
A("gen-workdir-observed-clean", "gen", "TestWorkingDirIsInTheManifest",
  r'''case s.WorkingDir != "" && path.Clean(s.WorkingDir) != path.Clean(cwd):''', r'''case s.WorkingDir != "" && path.Clean(s.WorkingDir) != cwd:''')
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
  r'''const mode = fs.FileMode(0o600)''', r'''const mode = fs.FileMode(0o644)''')
A("output-manifest-without-env-is-0644", "output", "TestWriteManifestPassesValidation|TestManifestPassthroughIsWrittenAndValid",
  r'''»ManifestPassthrough map[string]any⏎}''', r'''}''')
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
A("observe-user-root-zero", "observe", "TestObserveUserOfTheImageRunsTheProgram|TestObserveUserIsResolvedThroughPasswd",
  r'''if uid == 0 {''', r'''if false && uid == 0 {''')
A("observe-user-group-cut", "observe", "TestObserveUserOfTheImageRunsTheProgram|TestObserveUserIsResolvedThroughPasswd",
  r'''who, _, _ := strings.Cut(user, ":")''', r'''who := user''')
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
  r'''trace.TouchedPaths(raw, startDir, from)''', r'''trace.TouchedPaths(raw, startDir, 0)''')
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
  r'''sort.Strings(keys)⏎»entries := make([]string, 0, len(keys))''', r'''entries := make([]string, 0, len(keys))''')
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
  r'''} else if ia != arch {''', r'''} else if false {''')
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

# ------------------------------------------- mutations for the tests of the modules branch
# observe: start directory, user, stopped early, warnings, log
A("observe-start-dir-is-the-container-dir", "observe", "TestObserveTraceStartsInTheDirectoryOfTheContainer",
  r'''startDir := workingDir''', r'''startDir := "/"''')
A("observe-start-dir-empty-is-root", "observe", "TestObserveRelativePathWithoutAWorkingDirIsReadAgainstRoot",
  r'''if startDir == "" {''', r'''if false && startDir == "" {''')
A("observe-user-option-replaces-image", "observe", "TestObserveUserIsResolvedThroughPasswd",
  r'''imageUser = r.o.User''', r'''_ = r.o.User''')
A("observe-passwd-name-by-uid", "observe", "TestObserveUserIsResolvedThroughPasswd",
  r'''err == nil && n == uid {''', r'''err == nil && n >= 0 {''')
A("observe-stopped-is-noted", "observe", "TestObserveStoppedEarlyIsReported|TestObserveRunningUntilTheEndIsNotStoppedEarly",
  r'''if stopped {⏎»»if err := r.noteStopped(ctx, id); err != nil {''', r'''if stopped && false {⏎»»if err := r.noteStopped(ctx, id); err != nil {''')
A("observe-stopped-exit-code", "observe", "TestObserveStoppedEarlyIsReported",
  r'''r.stopped, r.exitCode = true, code''', r'''r.stopped, r.exitCode = true, 0''')
A("observe-stopped-log-redacted", "observe", "TestObserveStoppedEarlyIsReported",
  r'''if out, lerr := r.d.LogsTail(lctx, id, logLines); lerr == nil {⏎»»text := strings.TrimSpace(r.redact(string(out)))''',
  r'''if out, lerr := r.d.LogsTail(lctx, id, logLines); lerr == nil {⏎»»text := strings.TrimSpace(string(out))''')
A("observe-stopped-log-size", "observe", "TestObserveStoppedLogIsTheLastKiB",
  r'''if len(text) > stoppedLogBytes {''', r'''if len(text) > stoppedLogBytes*4 {''')
A("observe-stopped-first-lines", "observe", "TestObserveStoppedEarlyIsReported",
  r'''if len(first) == 3 {''', r'''if len(first) == 4 {''')
A("observe-log-lines-asked", "observe", "TestObserveLogTailAsksForHundredLines|TestObserveStoppedEarlyIsReported",
  r'''logLines = 100''', r'''logLines = 50''')
A("observe-stopped-in-result", "observe", "TestObserveRunningUntilTheEndIsNotStoppedEarly",
  r'''StoppedEarly:       r.stopped,''', r'''StoppedEarly:       true,''')
A("observe-probe-failure-is-warned", "observe", "TestObserveProbeFailureWarnsOncePerPort",
  r'''r.warnOnce(r.redact(fmt.Sprintf("port %d could not be asked for /: %s", port, failed[port])))''', r'''_, _ = port, failed[port]''')
A("observe-redact-short-values", "observe", "TestObserveRedactLeavesShortAndImageValuesAlone",
  r'''ok && secretName(k) && len(v) >= minSecret && !r.imageValues[v] {''', r'''ok && secretName(k) && !r.imageValues[v] {''')
A("observe-redact-image-values", "observe", "TestObserveRedactLeavesShortAndImageValuesAlone",
  r'''ok && secretName(k) && len(v) >= minSecret && !r.imageValues[v] {''', r'''ok && secretName(k) && len(v) >= minSecret {''')
A("observe-failed-create-removed", "observe", "TestObserveFailedCreateIsRemovedByName",
  r'''_ = r.d.Remove(cctx, o.Name, true)''', r'''_ = cctx''')
A("observe-container-name", "observe", "TestObserveSuccess|TestObserveFailedCreateIsRemovedByName|TestStartCreateOptions",
  r'''o.Name = r.containerName()''', r'''o.Name = ""''')
A("observe-container-label", "observe", "TestObserveSuccess|TestStartCreateOptions|TestObserveUserIsResolvedThroughPasswd",
  r'''o.Labels = []string{ownLabel}''', r'''o.Labels = nil''')
A("observe-trace-file-link-not-read", "observe", "TestObserveTraceFileThatIsALinkIsNotRead",
  r'''if !st.Mode().IsRegular() {''', r'''if false && !st.Mode().IsRegular() {''')
A("observe-prober-body-limit", "observe", "TestHTTPProberReadsAtMostOneMiBOfTheBody",
  r'''probeBodyLimit = 1 << 20''', r'''probeBodyLimit = 1 << 40''')
A("observe-fetch-relative-path", "observe", "TestFetchRefusesARelativePath",
  r'''if !path.IsAbs(p) {⏎»»return fetched{}, fmt.Errorf("the path %q is not absolute", p)''', r'''if false {⏎»»return fetched{}, fmt.Errorf("the path %q is not absolute", p)''')
A("observe-created-gone-counted", "observe", "TestObserveCreatedAndGoneIsCountedNotWarned",
  r'''if r.created[p] && isNotFound(err) {''', r'''if false && r.created[p] && isNotFound(err) {''')
A("observe-gone-needs-created", "observe", "TestObserveGoneButNotCreatedStillWarns",
  r'''if r.created[p] && isNotFound(err) {''', r'''if isNotFound(err) {''')
A("observe-gone-needs-not-found", "observe", "TestObserveOtherCopyErrorOfCreatedFileStillWarns",
  r'''if r.created[p] && isNotFound(err) {''', r'''if r.created[p] {''')
A("observe-not-carried-warn-limit", "observe", "TestObserveNotCarriedWarningsAreAggregated",
  r'''if r.notCarried <= maxWarnedPaths {''', r'''if true {''')
A("observe-not-carried-more", "observe", "TestObserveNotCarriedWarningsAreAggregated",
  r'''if r.notCarried > maxWarnedPaths {''', r'''if false && r.notCarried > maxWarnedPaths {''')
A("observe-not-carried-more-count", "observe", "TestObserveNotCarriedWarningsAreAggregated",
  r'''r.notCarried-maxWarnedPaths))''', r'''r.notCarried))''')
A("observe-created-warning-limit", "observe", "TestObserveCreatedWarningListsTenPaths",
  r'''list = list[:maxWarnedPaths]''', r'''list = list[:maxWarnedPaths+1]''')
A("observe-created-more-count", "observe", "TestObserveCreatedWarningListsTenPaths",
  r'''len(list)-maxWarnedPaths)''', r'''len(list))''')
A("observe-alias-not-carried-warned", "observe", "TestObserveAliasOfAPathThatWasNotCarriedIsAWarning",
  r'''case done[link] && !carried[link]:''', r'''case false:''')
A("observe-carried-is-recorded", "observe", "TestObserveAliasOfAPathThatWasNotCarriedIsAWarning|TestObserveAliasOfACarriedPathIsACopy",
  r'''carried[p] = true''', r'''_ = carried''')
A("observe-children-from-the-program", "observe", "TestObserveCountsChildProcesses|TestObserveForksBeforeTheProgramStartedAreNotChildren",
  r'''children := trace.ChildProcesses(raw, from)''', r'''children := trace.ChildProcesses(raw, 0)''')
A("observe-child-calls-distinct", "observe", "TestObserveCountsChildProcesses",
  r'''if !slices.Contains(childCalls, c.Call) {''', r'''if true {''')
A("observe-children-count", "observe", "TestObserveCountsChildProcesses",
  r'''len(children),''', r'''0,''')
A("observe-network-passed", "observe", "TestObservePassesTheNetworkToTheTracedContainer",
  r'''Network:        r.o.Network,''', r'''Network:        "",''')
A("observe-network-aliases-passed", "observe", "TestObservePassesTheNetworkToTheTracedContainer",
  r'''NetworkAliases: r.o.NetworkAliases,''', r'''NetworkAliases: nil,''')
# observe: Start
A("observe-start-network", "observe", "TestStartCreateOptions",
  r'''Network:        o.Network,''', r'''Network:        "",''')
A("observe-start-network-aliases", "observe", "TestStartCreateOptions",
  r'''NetworkAliases: o.NetworkAliases,''', r'''NetworkAliases: nil,''')
A("observe-start-binds", "observe", "TestStartCreateOptions",
  r'''Binds:          o.Binds,''', r'''Binds:          nil,''')
A("observe-start-user", "observe", "TestStartCreateOptions",
  r'''User:           o.User,''', r'''User:           "",''')
A("observe-start-workdir", "observe", "TestStartCreateOptions",
  r'''WorkingDir:     o.WorkingDir,''', r'''WorkingDir:     "",''')
A("observe-start-env", "observe", "TestStartCreateOptions",
  r'''Env:            o.Env,''', r'''Env:            nil,''')
A("observe-start-entrypoint", "observe", "TestStartCreateOptions",
  r'''Entrypoint:     o.Entrypoint,''', r'''Entrypoint:     nil,''')
A("observe-start-cmd", "observe", "TestStartCreateOptions",
  r'''Cmd:            o.Cmd,''', r'''Cmd:            nil,''')
A("observe-start-ready-when-ports-open", "observe", "TestStartReadyWhenEveryPortAccepts|TestStartNotReadyWithinWait",
  r'''} else if r.portsOpen(ctx, id, ports, o) {''', r'''} else if true {''')
A("observe-start-every-port", "observe", "TestStartReadyWhenEveryPortAccepts",
  r'''if err := o.dial(ctx, net.JoinHostPort(host, strconv.Itoa(p))); err != nil {''', r'''if err := o.dial(ctx, net.JoinHostPort(host, strconv.Itoa(p))); err != nil && false {''')
A("observe-start-stay-up-for", "observe", "TestStartImageWithoutPortsStaysUpThreeSeconds",
  r'''stayUpFor = 3 * time.Second''', r'''stayUpFor = 1 * time.Second''')
A("observe-start-stay-up-check", "observe", "TestStartImageWithoutPortsStaysUpThreeSeconds",
  r'''if !o.now().Before(begin.Add(stayUpFor)) {''', r'''if true {''')
A("observe-start-exits-before-ready", "observe", "TestStartExitsBeforeReady|TestStartImageWithoutPortsStaysUpThreeSeconds",
  r'''if !up {''', r'''if !up && false {''')
A("observe-start-deadline", "observe", "TestStartNotReadyWithinWait",
  r'''deadline := begin.Add(o.Wait)''', r'''deadline := begin.Add(o.Wait + time.Second)''')
A("observe-start-ports-in-the-error", "observe", "TestStartNotReadyWithinWait",
  r'''what = "did not accept connections on " + portList(ports)''', r'''what = "did not accept connections"''')
A("observe-start-log-bytes", "observe", "TestStartExitsBeforeReady",
  r'''startLogBytes = 1024''', r'''startLogBytes = 4096''')
A("observe-start-log-redacted", "observe", "TestStartExitsBeforeReady",
  r'''text := strings.TrimSpace(r.redact(string(out)))⏎»if len(text) > startLogBytes {''', r'''text := strings.TrimSpace(string(out))⏎»if len(text) > startLogBytes {''')
A("observe-start-failure-removes-the-container", "observe", "TestStartNotReadyWithinWait|TestStartExitsBeforeReady|TestStartCancelRemovesTheContainer",
  r'''if err := r.ready(ctx, id, ports, o); err != nil {⏎»»return fail(err)''', r'''if err := r.ready(ctx, id, ports, o); err != nil {⏎»»return nil, err''')
A("observe-start-stop-once", "observe", "TestRunningStopRemovesEvenWhenStopFails",
  r'''r.id = ""''', r'''_ = r.id''')
A("observe-start-stop-failure-still-removes", "observe", "TestRunningStopRemovesEvenWhenStopFails",
  r'''errs = append(errs, fmt.Errorf("stopping container %s: %w", id, err))''', r'''return fmt.Errorf("stopping container %s: %w", id, err)''')
A("observe-start-pull-rule", "observe", "TestStartImageMissingWithoutPull",
  r'''if !o.Pull {''', r'''if false {''')
A("observe-start-pulls", "observe", "TestStartImageMissingWithoutPull",
  r'''if err := d.Pull(ctx, o.Image); err != nil {''', r'''if err := error(nil); err != nil {''')
A("observe-start-default-wait", "observe", "TestStartReadyWhenEveryPortAccepts",
  r'''defaultStartWait = 60 * time.Second''', r'''defaultStartWait = 1 * time.Second''')
A("observe-start-poll-every", "observe", "TestStartReadyWhenEveryPortAccepts|TestStartNotReadyWithinWait",
  r'''startPollEvery = time.Second''', r'''startPollEvery = 2 * time.Second''')
# trace
A("trace-whereis-skips-dotdot", "trace", "TestWhereIsSkipsPathEntriesThatClimbOut",
  r'''if slices.Contains(strings.Split(dir, "/"), "..") {''', r'''if false && slices.Contains(strings.Split(dir, "/"), "..") {''')
A("trace-whereis-dotdot-inside-an-entry", "trace", "TestWhereIsSkipsPathEntriesThatClimbOut",
  r'''slices.Contains(strings.Split(dir, "/"), "..")''', r'''strings.HasPrefix(dir, "..")''')
A("trace-child-failed-call", "trace", "TestChildProcessesCalls|TestChildProcessesUnfinished",
  r'''if err != nil || pid <= 0 {''', r'''if err != nil {''')
A("trace-child-parentage", "trace", "TestChildProcessesFromAndAncestry",
  r'''parent[child] = caller''', r'''_ = child''')
A("trace-child-thread-is-not-a-process", "trace", "TestChildProcessesCalls|TestChildProcessesUnfinished",
  r'''isProcess := call == "fork" || call == "vfork" || !threadFlag.MatchString(callText)''', r'''isProcess := true''')
A("trace-child-thread-flag-on-resumed-part", "trace", "TestChildProcessesUnfinished",
  r'''!threadFlag.MatchString(callText)''', r'''!threadFlag.MatchString(text)''')
A("trace-child-unfinished-join", "trace", "TestChildProcessesUnfinished",
  r'''callText = pending[caller] + text''', r'''callText = text''')
A("trace-child-unfinished-is-held", "trace", "TestChildProcessesUnfinished",
  r'''} else if strings.HasSuffix(strings.TrimSpace(text), "<unfinished ...>") {''', r'''} else if false {''')
A("trace-child-from-is-inclusive", "trace", "TestChildProcessesFromAndAncestry",
  r'''c.child.Line < from {''', r'''c.child.Line <= from {''')
A("trace-child-threads-are-left-out", "trace", "TestChildProcessesCalls",
  r'''if !c.process || c.child.Line < from {''', r'''if c.child.Line < from {''')
A("trace-child-descendants-only", "trace", "TestChildProcessesFromAndAncestry",
  r'''if found && !reaches(parent, c.child.Parent, final.Pid) {''', r'''if false && found && !reaches(parent, c.child.Parent, final.Pid) {''')
A("trace-reaches-target-itself", "trace", "TestChildProcessesFromAndAncestry",
  r'''if pid == target {''', r'''if false {''')
# gen
A("gen-arch-checked", "gen", "TestArchIsChecked",
  r'''if in.Arch != "amd64" && in.Arch != "arm64" {''', r'''if false {''')
A("gen-arch-in-the-plan", "gen", "TestArchIsChecked",
  r'''plan: &plan.Plan{Arch: in.Arch}''', r'''plan: &plan.Plan{Arch: "amd64"}''')
A("gen-arch-in-the-compose-package", "gen", "TestArchIsChecked",
  r'''Local: true, Arch: in.Arch})''', r'''Local: true, Arch: "amd64"})''')
A("gen-cwd-unknown-is-root", "gen", "TestWorkingDirIsInTheManifest|TestEveryServiceHasACwd",
  r'''if cwd == "" {⏎»»cwd = "/"''', r'''if false {⏎»»cwd = "/"''')
A("gen-cwd-root-is-silent", "gen", "TestWorkingDirIsInTheManifest",
  r'''case cwd == "/":''', r'''case false:''')
A("gen-cwd-file-agrees-is-converted", "gen", "TestWorkingDirIsInTheManifest",
  r'''case s.WorkingDir != "":⏎»»g.add(s.Name, plan.Converted''', r'''case false:⏎»»g.add(s.Name, plan.Converted''')
A("gen-ports-remap-message", "gen", "TestPorts",
  r'''p.Published, p.Target, p.Target, pkg, p.Published))''', r'''p.Published, p.Target, p.Published, pkg, p.Published))''')
A("gen-ports-tcp-lists-the-target", "gen", "TestPorts",
  r'''m.RunConfig.Ports = append(m.RunConfig.Ports, p.Target)''', r'''m.RunConfig.Ports = append(m.RunConfig.Ports, p.Published)''')
A("gen-ports-range-message", "gen", "TestPorts",
  r'''it is not published", p.Published, p.Target))''', r'''it is not published", p.Target, p.Published))''')
A("gen-ports-host-ip-goes-on", "gen", "TestPortsTwoKindsAtOnce",
  r'''on every address", p.HostIP, p.Published))⏎»»}''', r'''on every address", p.HostIP, p.Published))⏎»»»continue⏎»»}''')
A("gen-stopped-early-item", "gen", "TestStoppedEarlyIsReported",
  r'''if obs.StoppedEarly {''', r'''if false && obs.StoppedEarly {''')
A("gen-stopped-early-exit-code", "gen", "TestStoppedEarlyIsReported",
  r'''the last one it started", obs.ExitCode)''', r'''the last one it started", 0)''')
A("gen-stopped-early-three-lines", "gen", "TestStoppedEarlyIsReported",
  r'''firstLines(obs.StoppedLog, 3)''', r'''firstLines(obs.StoppedLog, 4)''')
A("gen-first-lines-skips-empty", "gen", "TestStoppedEarlyIsReported",
  r'''if l = strings.TrimSpace(l); l == "" {''', r'''if l = strings.TrimSpace(l); false {''')
A("gen-child-processes-item", "gen", "TestChildProcessesAreUnsupported",
  r'''if obs.Children > 0 {''', r'''if false && obs.Children > 0 {''')
A("gen-child-processes-count", "gen", "TestChildProcessesAreUnsupported",
  r'''see the documentation of the program", obs.Children))''', r'''see the documentation of the program", 1))''')
A("gen-vanished-item", "gen", "TestVanishedFilesAreANote",
  r'''if obs.Vanished > 0 {''', r'''if false && obs.Vanished > 0 {''')
A("gen-vanished-count", "gen", "TestVanishedFilesAreANote",
  r'''not in the package", obs.Vanished))''', r'''not in the package", 1))''')
A("gen-vanished-is-a-note", "gen", "TestVanishedFilesAreANote",
  r'''g.add(s.Name, plan.Note, "vanished",''', r'''g.add(s.Name, plan.Observed, "vanished",''')
A("gen-argv0-stays", "gen", "TestArgv0StaysAsTheProgramSawIt",
  r'''m.Args = slices.Clone(obs.Argv)''', r'''m.Args = slices.Clone(obs.Argv)⏎»if len(m.Args) > 0 {⏎»»m.Args[0] = obs.Program⏎»}''')
# docker
A("docker-passenv-last-value-wins", "docker", "TestCreateSecretsStayOffTheCommandLine",
  r'''env[i] = name + "=" + val''', r'''_ = i''')
A("docker-passenv-empty-name", "docker", "TestCreateBadEnvName",
  r'''name == "" || strings.ContainsRune(name, 0)''', r'''strings.ContainsRune(name, 0)''')
A("docker-passenv-nul-in-name", "docker", "TestCreateBadEnvName",
  r'''strings.ContainsRune(name, 0) || strings.ContainsRune(val, 0)''', r'''strings.ContainsRune(val, 0)''')
A("docker-passenv-nul-in-value", "docker", "TestCreateBadEnvName",
  r'''|| strings.ContainsRune(val, 0) {''', r'''{''')
A("docker-env-needs-env-runner", "docker", "TestEnvNeedsEnvRunner",
  r'''if !ok {⏎»»return nil, nil, fmt.Errorf("%s: the runner cannot pass environment variables''', r'''if !ok && false {⏎»»return nil, nil, fmt.Errorf("%s: the runner cannot pass environment variables''')
A("docker-plain-run-without-env", "docker", "TestEnvNeedsEnvRunner",
  r'''if len(env) == 0 {⏎»»stdout, stderr, err := c.r.Run(ctx, nil, args...)''', r'''if false {⏎»»stdout, stderr, err := c.r.Run(ctx, nil, args...)''')
A("docker-env-is-passed", "docker", "TestCreateSecretsStayOffTheCommandLine|TestBuildSecretsStayOffTheCommandLine",
  r'''return er.RunEnv(ctx, env, nil, args...)''', r'''return er.RunEnv(ctx, nil, nil, args...)''')
A("docker-build-arg-value-in-env", "docker", "TestBuildSecretsStayOffTheCommandLine",
  r'''entries = append(entries, k+"="+o.Args[k])''', r'''entries = append(entries, k)''')
A("docker-build-arg-name-equals", "docker", "TestBuildArgsBadName",
  r'''strings.ContainsAny(k, "=\x00")''', r'''strings.ContainsAny(k, "\x00")''')
A("docker-build-arg-empty-name", "docker", "TestBuild|TestBuildArgsBadName",
  r'''if k == "" {''', r'''if false {''')
A("docker-build-error-exit-error-redacted", "docker", "TestBuildErrorHidesArgumentValues",
  r'''ee.Stderr = redactSecrets(ee.Stderr, vals)''', r'''_ = vals''')
A("docker-build-error-stderr-redacted", "docker", "TestBuildErrorHidesArgumentValues",
  r'''[]byte(redactSecrets(string(stderr), vals))''', r'''stderr''')
A("docker-redact-short-values-kept", "docker", "TestBuildErrorHidesArgumentValues",
  r'''if len(v) >= minSecret {''', r'''if len(v) >= 0 {''')
A("docker-exec-runner-env-added-to-environment", "docker", "TestExecRunnerRunEnv",
  r'''cmd.Env = append(os.Environ(), env...)''', r'''cmd.Env = append(os.Environ()[:0], env...)''')
A("docker-exec-runner-env-set", "docker", "TestExecRunnerRunEnv",
  r'''if len(env) > 0 {⏎»»cmd.Env''', r'''if false {⏎»»cmd.Env''')
A("docker-network-aliases-need-network", "docker", "TestCreateArgsNetworkGuards",
  r'''if len(o.NetworkAliases) > 0 && o.Network == "" {''', r'''if len(o.NetworkAliases) > 0 && o.Network == "" && false {''')
A("docker-network-option-like", "docker", "TestCreateArgsNetworkGuards",
  r'''if err := checkArg("docker create", "network", o.Network); err != nil {''', r'''if err := checkArg("docker create", "network", o.Network); err != nil && false {''')
A("docker-network-alias-option-like", "docker", "TestCreateArgsNetworkGuards",
  r'''if err := checkArg("docker create", "network alias", al); err != nil {''', r'''if err := checkArg("docker create", "network alias", al); err != nil && false {''')
A("docker-network-flag", "docker", "TestCreateArgsNetworkAliases",
  r'''args = append(args, "--network", o.Network)''', r'''args = append(args, "--net", o.Network)''')
A("docker-network-alias-flag", "docker", "TestCreateArgsNetworkAliases",
  r'''args = append(args, "--network-alias", al)''', r'''args = append(args, "--alias", al)''')
A("docker-network-create-argv", "docker", "TestNetworkCommands",
  r'''[]string{"network", "create", name}''', r'''[]string{"network", "create"}''')
A("docker-network-rm-argv", "docker", "TestNetworkCommands",
  r'''[]string{"network", "rm", name}''', r'''[]string{"network", "remove", name}''')
A("docker-network-create-bad-name", "docker", "TestNetworkCommandsRefuseBadNames",
  r'''if err := checkArg("docker network create", "network name", name); err != nil {''', r'''if err := checkArg("docker network create", "network name", name); err != nil && false {''')
A("docker-network-rm-bad-name", "docker", "TestNetworkCommandsRefuseBadNames",
  r'''if err := checkArg("docker network rm", "network name", name); err != nil {''', r'''if err := checkArg("docker network rm", "network name", name); err != nil && false {''')
A("docker-network-create-failure-wrapped", "docker", "TestNetworkCreateFailureIsWrapped",
  r'''_, err := c.do(ctx, "docker network create", []string{"network", "create", name})''', r'''_, _, err := c.r.Run(ctx, nil, "network", "create", name)''')
# plan, output
A("plan-passthrough-is-written", "plan", "TestManifestJSONFull",
  r'''ManifestPassthrough map[string]any `json:",omitempty"`''', r'''ManifestPassthrough map[string]any `json:"-"`''')
A("plan-passthrough-nil-is-omitted", "plan", "TestManifestJSONFull|TestManifestJSONAlwaysWritesWhatOpsKeepsBetweenPackages",
  r'''ManifestPassthrough map[string]any `json:",omitempty"`''', r'''ManifestPassthrough map[string]any `json:"ManifestPassthrough"`''')
# cmd
A("cmd-arch-x86-64", "cmd", "TestPackageArch",
  r'''case "amd64", "x86_64":''', r'''case "amd64":''')
A("cmd-arch-aarch64", "cmd", "TestPackageArch",
  r'''case "arm64", "aarch64":''', r'''case "arm64":''')
A("cmd-arch-error-names-the-variant", "cmd", "TestUnsupportedImageArchitectureIsAnError",
  r'''name += "/" + variant''', r'''name += variant''')
A("cmd-arch-error-is-returned", "cmd", "TestUnsupportedImageArchitectureIsAnError",
  r'''ia, err := packageArch(si.Image.Architecture, si.Image.Variant)⏎»»»if err != nil {''', r'''ia, err := packageArch(si.Image.Architecture, si.Image.Variant)⏎»»»if err != nil && false {''')
A("cmd-observe-user", "cmd", "TestObserveOptions",
  r'''oo.User = s.User''', r'''oo.User = ""''')
A("cmd-observe-working-dir", "cmd", "TestObserveOptions",
  r'''oo.WorkingDir = s.WorkingDir''', r'''oo.WorkingDir = ""''')
A("cmd-observe-seconds", "cmd", "TestObserveOptions",
  r'''oo.Seconds = o.seconds''', r'''oo.Seconds = 0''')
A("cmd-observe-network", "cmd", "TestObserveOptions|TestNetworkCreatedOnceAndRemoved",
  r'''oo.Network = rs.network''', r'''oo.Network = ""''')
A("cmd-observe-network-alias", "cmd", "TestObserveOptions|TestNetworkCreatedOnceAndRemoved",
  r'''oo.NetworkAliases = []string{s.Name}''', r'''oo.NetworkAliases = nil''')
A("cmd-signal-sighup", "cmd", "TestSecondSignalTerminates",
  r'''signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)''', r'''signal.Notify(ch, os.Interrupt, syscall.SIGTERM)''')
A("cmd-signal-sigterm", "cmd", "TestSecondSignalTerminates",
  r'''signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)''', r'''signal.Notify(ch, os.Interrupt, syscall.SIGHUP)''')
A("cmd-signal-interrupt", "cmd", "TestSecondSignalTerminates",
  r'''signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)''', r'''signal.Notify(ch, syscall.SIGTERM, syscall.SIGHUP)''')
A("cmd-signal-stop-before-cancel", "cmd", "TestSecondSignalTerminates",
  r'''case <-ch:⏎»»»signal.Stop(ch)⏎»»»cancel()''', r'''case <-ch:⏎»»»cancel()''')
A("cmd-next-steps-ops-home-prefix", "cmd", "TestOutputAndNextSteps|TestOpsHomeIsAbsolute",
  r'''if home != "" {''', r'''if false {''')
A("cmd-next-steps-written", "cmd", "TestOutputAndNextSteps",
  r'''fmt.Fprintln(a.Stdout, "Written:")''', r'''fmt.Fprintln(a.Stdout, "Wrote:")''')
A("cmd-next-steps-compose-up", "cmd", "TestOutputAndNextSteps",
  r'''ops compose up -f %s''', r'''ops compose up %s''')
A("cmd-next-steps-dns-package", "cmd", "TestOutputAndNextSteps",
  r'''const opsDNSPackage = "eyberg/ops-dns:0.0.1"''', r'''const opsDNSPackage = "eyberg/ops-dns:0.0.2"''')
A("cmd-shell-quote-slash", "cmd", "TestOutputAndNextSteps",
  r'''0123456789_-./:=+,@%''', r'''0123456789_-.:=+,@%''')
A("cmd-ops-home-flag", "cmd", "TestOutputAndNextSteps|TestOpsHomeIsAbsolute",
  r'''h := o.opsHome''', r'''h := ""''')
A("cmd-ops-home-environment", "cmd", "TestOutputAndNextSteps|TestOpsHomeIsAbsolute",
  r'''if v, ok := a.LookupEnv("OPS_HOME"); ok && v != "" {''', r'''if v, ok := a.LookupEnv("OPS_HOME"); false && ok && v != "" {''')
A("cmd-ops-home-user-home", "cmd", "TestOutputAndNextSteps",
  r'''if h == "" && a.UserHomeDir != nil {''', r'''if h == "" && a.UserHomeDir != nil && false {''')
A("cmd-ops-home-is-absolute", "cmd", "TestOpsHomeIsAbsolute",
  r'''return absPath(wd, h)''', r'''return h''')
A("cmd-relative-current-directory", "cmd", "TestRelativeCurrentDirectoryIsRefused",
  r'''if !filepath.IsAbs(wd) {''', r'''if false {''')
A("cmd-bind-target-is-absolute", "cmd", "TestBindWithRelativeTargetIsNotPassed",
  r'''if !filepath.IsAbs(host) || !strings.HasPrefix(v.Target, "/") {''', r'''if !filepath.IsAbs(host) {''')
A("cmd-no-network-when-nothing-is-converted", "cmd", "TestNoNetworkWhenNothingIsConverted",
  r'''if len(converted) > 0 {⏎»»name, err := networkName()''', r'''if true {⏎»»name, err := networkName()''')
A("cmd-network-create-failure", "cmd", "TestNetworkCreateFailureStopsTheRun",
  r'''if err := a.Docker.NetworkCreate(ctx, name); err != nil {''', r'''if err := a.Docker.NetworkCreate(ctx, name); err != nil && false {''')
A("cmd-network-removed-with-its-own-context", "cmd", "TestNetworkAndDependenciesAreCleanedUpOnCancel",
  r'''cctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)⏎»»»defer cancel()⏎»»»if err := a.Docker.NetworkRemove(cctx, name)''',
  r'''cctx, cancel := context.WithTimeout(ctx, cleanupTimeout)⏎»»»defer cancel()⏎»»»if err := a.Docker.NetworkRemove(cctx, name)''')
A("cmd-network-removed-by-name", "cmd", "TestNetworkCreatedOnceAndRemoved",
  r'''a.Docker.NetworkRemove(cctx, name)''', r'''a.Docker.NetworkRemove(cctx, name+"x")''')
A("cmd-network-name-prefix", "cmd", "TestNetworkCreatedOnceAndRemoved",
  r'''return "dock2nano-" + hex.EncodeToString(b), nil''', r'''return "d2n-" + hex.EncodeToString(b), nil''')
A("cmd-dependencies-stopped", "cmd", "TestDependenciesStartedOnceInOrderAndStoppedAtTheEnd|TestNetworkAndDependenciesAreCleanedUpOnError",
  r'''defer rs.stopAll(a)''', r'''_ = rs''')
A("cmd-dependencies-stopped-in-reverse", "cmd", "TestDependenciesStartedOnceInOrderAndStoppedAtTheEnd",
  r'''for i := len(rs.order) - 1; i >= 0; i-- {''', r'''for i := 0; i < len(rs.order); i++ {''')
A("cmd-dependencies-started-once", "cmd", "TestDependenciesStartedOnceInOrderAndStoppedAtTheEnd",
  r'''if rs.running[name] != nil {''', r'''if rs.running[name] != nil && false {''')
A("cmd-dependencies-recorded-as-running", "cmd", "TestDependenciesStartedOnceInOrderAndStoppedAtTheEnd",
  r'''rs.order = append(rs.order, name)''', r'''_ = name''')
A("cmd-dependencies-start-on-the-network", "cmd", "TestDependenciesStartedOnceInOrderAndStoppedAtTheEnd",
  r'''so.Network = rs.network''', r'''so.Network = ""''')
A("cmd-dependencies-start-under-their-name", "cmd", "TestDependenciesStartedOnceInOrderAndStoppedAtTheEnd",
  r'''so.NetworkAliases = []string{name}''', r'''so.NetworkAliases = nil''')
A("cmd-dependencies-start-env", "cmd", "TestDependenciesStartedOnceInOrderAndStoppedAtTheEnd",
  r'''Env: oo.Env, WorkingDir: oo.WorkingDir''', r'''WorkingDir: oo.WorkingDir''')
A("cmd-dependencies-start-working-dir", "cmd", "TestDependenciesStartedOnceInOrderAndStoppedAtTheEnd",
  r'''WorkingDir: oo.WorkingDir, User: oo.User''', r'''User: oo.User''')
A("cmd-dependencies-start-user", "cmd", "TestDependenciesStartedOnceInOrderAndStoppedAtTheEnd",
  r'''User: oo.User, Binds: oo.Binds}''', r'''Binds: oo.Binds}''')
A("cmd-dependency-failure-is-remembered", "cmd", "TestDependencyStartFailureIsANote",
  r'''why := rs.failed[name]''', r'''why := ""''')
A("cmd-dependency-note-reaches-the-observation", "cmd", "TestDependencyStartFailureIsANote|TestDependencyNotConvertedIsANote",
  r'''res.Warnings = append(res.Warnings, notes...)''', r'''_ = notes''')
A("cmd-dependency-not-converted-reason", "cmd", "TestDependencyNotConvertedIsANote",
  r'''if reason, no := rs.skipped[name]; no {''', r'''if reason, no := rs.skipped[name]; no && false {''')
A("cmd-dependency-cancel-is-an-error", "cmd", "TestCancelWhileStartingADependency",
  r'''if cerr := ctx.Err(); cerr != nil {⏎»»»»»return nil, cerr''', r'''if cerr := ctx.Err(); cerr != nil && false {⏎»»»»»return nil, cerr''')

# ---------------------------------------------------------------- rules that had no test
# observe
A("observe-redact-only-secret-names", "observe", "TestObserveRedactOnlyValuesOfSecretNames",
  r"""ok && secretName(k) && len(v)""", r"""ok && (secretName(k) || true) && len(v)""")
A("observe-redact-boundary-is-six", "observe", "TestObserveRedactHidesValuesFromSixBytes",
  r"""secretName(k) && len(v) >= minSecret &&""", r"""secretName(k) && len(v) > minSecret &&""")
A("observe-redact-min-secret-five", "observe", "TestObserveRedactHidesValuesFromSixBytes",
  r"""const minSecret = 6""", r"""const minSecret = 5""")
A("observe-passwd-skips-comment-lines", "observe", "TestPasswdNameSkipsCommentLines",
  r"""f[0] == "" || strings.HasPrefix(f[0], "#") {""", r"""f[0] == "" {""")
A("observe-user-too-many-digits", "observe", "TestResolveUserWithTooManyDigitsIsObservedAsRoot",
  r"""if strings.Trim(who, "0123456789") == "" {""", r"""if false {""")
A("observe-create-without-an-id", "observe", "TestCreateWithoutAnIDIsAnErrorAndIsRemovedByName",
  r"""if err != nil || id == "" {""", r"""if err != nil {""")
A("observe-stopped-log-whole-line", "observe", "TestObserveStoppedLogIsCutAtAWholeLine",
  r"""if i := strings.Index(text, "\n"); i >= 0 && i < len(text)-1 {""", r"""if i := strings.Index(text, "\n"); false && i >= 0 && i < len(text)-1 {""")
A("observe-stopped-log-valid-utf8", "observe", "TestObserveStoppedLogDropsABrokenCharacter",
  r"""text = strings.ToValidUTF8(text, "")""", r"""_ = text""")
A("observe-start-log-valid-utf8", "observe", "TestStartLogTailDropsABrokenCharacter",
  r"""text = strings.ToValidUTF8(text[len(text)-startLogBytes:], "")""", r"""text = text[len(text)-startLogBytes:]""")
A("observe-port-asked-later-does-not-warn", "observe", "TestObserveAPortThatFailedAndWasAskedLaterDoesNotWarn",
  r"""if !asked[port] {""", r"""if true {""")
A("observe-warn-once", "observe", "TestObserveARepeatedWarningIsGivenOnce",
  r"""if r.once[text] {""", r"""if false && r.once[text] {""")
A("observe-running-stop-own-context", "observe", "TestRunningStopRemovesEvenWhenStopFails",
  r"""cctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)⏎»defer cancel()⏎»var errs []error""",
  r"""cctx, cancel := context.WithTimeout(ctx, cleanupTimeout)⏎»defer cancel()⏎»var errs []error""")
A("observe-start-call-failure-removes-the-container", "observe", "TestStartFailureOfTheContainerRemovesIt",
  r"""return fail(fmt.Errorf("observe: start: starting the container: %w", err))""",
  r"""return nil, fmt.Errorf("observe: start: starting the container: %w", err)""")
A("observe-ports-open-needs-an-addresser", "observe", "TestStartWithoutAnAddresserIsNeverReady",
  r"""a, ok := r.d.(Addresser)⏎»if !ok {⏎»»return false""", r"""a, ok := r.d.(Addresser)⏎»if !ok && false {⏎»»return false""")
A("observe-ports-open-needs-an-address", "observe", "TestStartWithAnEmptyAddressIsNeverReady",
  r"""if err != nil || host == "" {⏎»»return false""", r"""if err != nil {⏎»»return false""")
A("observe-start-needs-an-image", "observe", "TestStartWithoutAnImageIsAnError",
  r"""if o.Image == "" {⏎»»return nil, errors.New("observe: start: no image given")""", r"""if false {⏎»»return nil, errors.New("observe: start: no image given")""")
# trace
A("trace-child-result-zero", "trace", "TestChildProcessesAResultOfZeroIsNotAChild",
  r"""if err != nil || pid <= 0 {""", r"""if err != nil || pid < 0 {""")
A("trace-child-thread-flag-word-end", "trace", "TestChildProcessesThreadFlagIsAWholeWord",
  r"""regexp.MustCompile(`\bCLONE_THREAD\b`)""", r"""regexp.MustCompile(`\bCLONE_THREAD`)""")
A("trace-child-thread-flag-word-start", "trace", "TestChildProcessesThreadFlagIsAWholeWord",
  r"""regexp.MustCompile(`\bCLONE_THREAD\b`)""", r"""regexp.MustCompile(`CLONE_THREAD\b`)""")
# without the guard the walk never ends: the run is cut by the timeout of go test, which is a failure
A("trace-reaches-pid-reuse-guard", "trace", "TestChildProcessesPidReuseDoesNotLoop",
  r"""for !seen[pid] {""", r"""for {""")
A("trace-child-resumed-forgets-its-start", "trace", "TestChildProcessesResumedLineWithoutItsStartIsNotJoinedToAnOldOne",
  r"""delete(pending, caller)""", r"""_ = pending""")
A("trace-child-unfinished-with-pid-prefix", "trace", "TestChildProcessesUnfinishedAndResumedWithPidPrefix",
  r"""pending[caller] = text""", r"""pending[m[2]] = text""")
# gen
A("gen-ports-unsupported-protocol-target", "gen", "TestPortMessagesNameTheContainerPortAndTheProtocol",
  r"""is not supported by ops; the port %s is not published", proto, p.Target))""", r"""is not supported by ops; the port %s is not published", proto, p.Published))""")
A("gen-ports-unsupported-protocol-name", "gen", "TestPortMessagesNameTheContainerPortAndTheProtocol",
  r"""is not supported by ops; the port %s is not published", proto, p.Target))""", r"""is not supported by ops; the port %s is not published", "tcp", p.Target))""")
A("gen-ports-not-published-target", "gen", "TestPortMessagesNameTheContainerPortAndTheProtocol",
  r"""is not published; it is reachable only from other services", p.Target, proto))""", r"""is not published; it is reachable only from other services", p.Published, proto))""")
A("gen-ports-not-published-protocol", "gen", "TestPortMessagesNameTheContainerPortAndTheProtocol",
  r"""is not published; it is reachable only from other services", p.Target, proto))""", r"""is not published; it is reachable only from other services", p.Target, "tcp"))""")
A("gen-stopped-early-empty-log", "gen", "TestStoppedEarlyWithoutALogHasNoLogSuffix",
  r"""head != "" {""", r"""true {""")
# docker
A("docker-redact-longer-first", "docker", "TestRedactSecretsHidesTheLongerValueWhole|TestBuildErrorHidesArgumentValues",
  r"""return len(long[i]) > len(long[j])""", r"""return len(long[i]) < len(long[j])""")
A("docker-passenv-valueless-entry", "docker", "TestEnvEntryWithoutAValueIsPassedByName",
  r"""args = append(args, flag, name)⏎»»if !has {""", r"""args = append(args, flag, name)⏎»»if !has && false {""")
A("docker-network-remove-failure-is-wrapped", "docker", "TestNetworkRemoveFailureIsWrapped",
  r"""_, err := c.do(ctx, "docker network rm", []string{"network", "rm", name})""", r"""_, _, err := c.r.Run(ctx, nil, "network", "rm", name)""")
# output
A("output-manifest-chmod", "output", "TestManifestModeIsSetWhateverTheUmask",
  r"""if err := os.Chmod(mpath, mode); err != nil {""", r"""if err := error(nil); err != nil {""")
# cmd
A("cmd-shell-quote-embedded-quote", "cmd", "TestShellQuote",
  r"""strings.ReplaceAll(s, "'", `'\''`)""", r"""strings.ReplaceAll(s, "'", "")""")
A("cmd-shell-quote-empty", "cmd", "TestShellQuote",
  r"""if s != "" && strings.Trim(""", r"""if strings.Trim(""")
A("cmd-shell-quote-safe-punctuation", "cmd", "TestShellQuote",
  r"""0123456789_-./:=+,@%""", r"""0123456789_-./""")
A("cmd-shell-quote-dollar-is-unsafe", "cmd", "TestShellQuote",
  r"""+,@%")""", r"""+,@%$")""")
A("cmd-closure-is-transitive", "cmd", "TestClosureHoldsTheDependenciesOfTheDependencies",
  r"""need[d.Service] = true⏎»»»»visit(d.Service)""", r"""need[d.Service] = true""")
A("cmd-network-not-removed-warning", "cmd", "TestNetworkRemoveFailureIsAWarning",
  r"""fmt.Fprintf(a.Stderr, "%s: warning: the docker network %s is not removed: %v\n", ToolName, name, err)""", r"""_ = err""")
A("cmd-stop-failure-warning", "cmd", "TestStopFailureOfADependencyIsAWarning",
  r"""fmt.Fprintf(a.Stderr, "%s: warning: stopping %s: %v\n", ToolName, name, err)""", r"""_ = err""")
A("cmd-starting-is-announced", "cmd", "TestStartingADependencyIsAnnounced",
  r"""fmt.Fprintf(a.Stderr, "%s: starting %s for %s\n", name, so.Image, s.Name)""", r"""_ = so.Image""")


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
