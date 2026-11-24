import sys
T='internal/trace/trace.go'; O='internal/observe/observe.go'; G='internal/gen/gen.go'; L='internal/trace/launcher.go'
R={
 'init-passover':[(T,'isInit := IsInit(last[process].Path)','isInit := false')],
 'init-names-catatonit-dockerinit':[(T,'|catatonit|docker-init','')],
 'init-arch-and-version-names':[(T,'(?:_[0-9][0-9.]*)?(?:[-_](?:amd64|arm64|armhf|armel|i386|ppc64le|s390x|x86_64|aarch64))?','')],
 'starter-roots':[(T,'if up := starter(process); up == "" || !isRunning[up] {','if up := parent[process]; up == "" || !isRunning[up] {'),(T,'if other != process && starter(other) == process {','if other != process && parent[other] == process {')],
 'starter-notrun':[(T,'		for child := range parent {\n			up := starter(child)\n			if !onChain[up]','		for child, up := range parent {\n			if !onChain[up]')],
 'starter-childlaunchers':[(T,'	for child := range parent {\n		if starter(child) != finalPid || child == finalPid {','	for child, up := range parent {\n		if up != finalPid || child == finalPid {')],
 'cwd-at-execve':[(O,'dir != "" && dir != cwd {\n		r.vlogf','false && dir != "" && dir != cwd {\n		r.vlogf')],
 'cwd-shared-by-threads':[(T,'cwd.share(child[3], pid)','cwd.copyOf(child[3], pid)')],
 'pid-changed-execve':[(T,'if m[5] != "" {\n				// a thread','if false && m[5] != "" {\n				// a thread'),(T,'|| m[5] != "" {','{ //')],
 'superseded-swap':[(T,'from, to := m[3], m[1]+m[2]','from, to := m[1]+m[2], m[3]')],
 'home-of-user':[(O,'if asUser != "" && home != "" && !slices','if false && asUser != "" && home != "" && !slices')],
 'named-user-lookup':[(O,'		name = who\n	} else if uid == 0 {','		return who, "", nil\n	} else if uid == 0 {')],
 'home-unknown-warning':[(O,'		if home == "" {\n			// docker','		if false && home == "" {\n			// docker')],
 'passwd-home':[(O,'func passwdHome(data []byte, name string) string {\n','func passwdHome(data []byte, name string) string {\n	name = ""\n')],
 'link-dir-outside-sysroot':[(T,'if !within(root, realDir) {','if false {')],
 'link-target-outside-sysroot':[(T,'if !within(root, target) {','if false {')],
 'link-to-itself':[(T,'if target == realDir || within(realDir, target)','if within(realDir, target)')],
 'link-into-own-subdir':[(T,'|| within(realDir, target) ||','||')],
 'link-to-ancestor':[(T,'|| within(target, realDir) {','{')],
 'link-clash':[(T,'if clash := mergeClash(realDir, target); clash != ""','if clash := ""; clash != ""')],
 'link-dir-modes':[(T,'				os.Chmod(made[i].path, made[i].mode)','				_ = made[i]')],
 'link-readonly-source':[(T,'if err := os.Chmod(p, info.Mode().Perm()|0700); err != nil {','if err := error(nil); err != nil {')],
 'newlink-parent-outside':[(T,'} else if !parentInside(sysroot, filepath.Dir(destination)) {','} else if false {')],
 'copy-parent-outside':[(T,'	if !parentInside(sysroot, filepath.Dir(destination)) {\n		return &LinkSkipped{to,','	if false {\n		return &LinkSkipped{to,')],
 'skipped-is-a-warning':[(O,'if errors.As(err, &left) {','if false && errors.As(err, &left) {')],
 'launcher-note-leaves-init-out':[(G,'		if !trace.IsInit(l) {\n			passed','		if true {\n			passed')],
 'npm-sentence-only-for-launchers':[(G,'launcher := passedLauncher(passed)','launcher := true')],
 'init-passed-over-note':[(G,'		if trace.IsInit(l) {\n			g.add','		if false && trace.IsInit(l) {\n			g.add')],
 'init-kept-item':[(G,'	if trace.IsInit(obs.Program) {\n		g.add(s.Name, plan.Unsupported, "init kept"','	if false && trace.IsInit(obs.Program) {\n		g.add(s.Name, plan.Unsupported, "init kept"')],
 'supervisor-message':[(G,'if trace.ProcessSupervisors[name] {','if false && trace.ProcessSupervisors[name] {')],
 'supervisor-by-name':[(L,'ProcessSupervisors[b] {','false && ProcessSupervisors[b] {')],
 'supervisor-python-option':[(L,'		if strings.HasPrefix(a, "-") {\n			continue\n		}','')],
}
name=sys.argv[1]
for f,old,new in R[name]:
    s=open(f).read()
    assert old in s,(name,f,old)
    open(f,'w').write(s.replace(old,new,1))
print("applied",name)
