import sys
T='internal/trace/trace.go'; O='internal/observe/observe.go'; G='internal/gen/gen.go'; L='internal/trace/launcher.go'
R={
 'nearest-rule-off':[(T,r'if program, others := nearestProgram(process); program != "" {','if program, others := nearestProgram(process); false && program != "" {')],
 'nearest-fork-order-decides':[(T,r'if a.path[k] != b.path[k] {','if false {')],
 'nearest-later-fork-wins':[(T,r'return a.path[k] > b.path[k]','return a.path[k] < b.path[k]')],
 'nearest-nearer-wins':[(T,r'c.depth < found[best].depth ||','c.depth > found[best].depth ||')],
 'nearest-shells-are-not-the-program':[(T,r'c.notProgram = c.isShell || ','c.notProgram = ')],
 'nearest-shell-by-file-name':[(T,r'c.isShell = shells[interp] || shells[name]','c.isShell = shells[interp]')],
 'nearest-busybox-is-not-the-program':[(T,r' || name == "busybox"','')],
 'nearest-ps-is-not-the-program':[(T,r' || name == "ps"','')],
 'nearest-waiters-are-not-the-program':[(T,'waiters[name] || ','(false && waiters[name]) || ')],
 'nearest-copiers-are-not-the-program':[(T,'copiers[name] || ','(false && copiers[name]) || ')],
 'nearest-sleep-in-the-list':[(T,r'"sleep": true, ','')],
 'nearest-tail-in-the-list':[(T,r'"tail": true, ','')],
 'nearest-watch-in-the-list':[(T,r', "watch": true}','}')],
 'nearest-tee-in-the-list':[(T,r'"tee": true, ','')],
 'nearest-cat-in-the-list':[(T,r'"cat": true, ','')],
 'nearest-logger-in-the-list':[(T,r'"logger": true, ','')],
 'nearest-rotatelogs-in-the-list':[(T,r'"rotatelogs": true, ','')],
 'nearest-cronolog-in-the-list':[(T,r'"cronolog": true, ','')],
 'nearest-svlogd-in-the-list':[(T,r'"svlogd": true, ','')],
 'nearest-multilog-in-the-list':[(T,r', "multilog": true}','}')],
 'nearest-zsh-is-a-shell':[(T,r', "zsh": true','')],
 'nearest-ksh-is-a-shell':[(T,r', "ksh": true','')],
 'nearest-mksh-is-a-shell':[(T,r', "mksh": true','')],
 'nearest-supervisors-are-not-the-program':[(T,r'if tier == 0 && (c.notProgram || c.supervisor) ||','if tier == 0 && c.notProgram ||')],
 'nearest-supervisor-when-nothing-else':[(T,r'for tier := 0; tier < 2 && best < 0; tier++ {','for tier := 0; tier < 1 && best < 0; tier++ {')],
 'nearest-under-a-supervisor-is-its-own':[(T,r'c.under = true','c.under = false')],
 'nearest-exited-are-not-candidates':[(T,'for _, pid := range running {\n			if pid == shell {','for _, pid := range all {\n			if pid == shell {')],
 'nearest-only-for-a-shell':[(T,r'len(children) > 1 && shells[path.Base(last[process].Path)] && !launcher && !isInit {','len(children) > 1 && (shells[path.Base(last[process].Path)] || isInit) {')],
 'nearest-subshell-is-the-shell':[(T,'				up := starter(x)\n				if up == "" {\n					break\n				}\n				c.depth++','				up := parent[x]\n				if up == "" {\n					break\n				}\n				c.depth++')],
 'beside-recorded':[(T,r'beside = append(beside, besideLabel(last[o].Path, last[o].Argv))','_ = o')],
 'beside-by-the-plain-label':[(T,r'besideLabel(last[o].Path, last[o].Argv)','Label(last[o].Path, last[o].Argv)')],
 'beside-label-java':[(L,r'case name == "java":','case false:')],
 'beside-label-interpreters':[(L,r'case interpreterName.MatchString(name):','case false:')],
 'beside-label-module':[(L,r'case a == "-m" && i+1 < len(args):','case false:')],
 'beside-leaves-out-what-is-below-the-program':[(T,r'k == best || c.isShell || c.under || slices.Contains(c.chain, chosen)','k == best || c.isShell || c.under')],
 'beside-leaves-out-shells':[(T,r'k == best || c.isShell ||','k == best ||')],
 'beside-leaves-out-what-is-under-a-supervisor':[(T,r'k == best || c.isShell || c.under ||','k == best || c.isShell ||')],
 'beside-handed-on':[(T,r'	final.Beside = beside','')],
 'beside-carried-by-observe':[(O,r'		Beside:             final.Beside,','')],
 'beside-item-in-the-report':[(G,r'if len(obs.Beside) > 0 {','if false && len(obs.Beside) > 0 {')],
 'beside-wording':[(G,r'(the branch the shell forked last, at equal distance)','(the later one of equal distance)')],
 'hub-whole-names-beside':[('internal/gen/hub.go','if len(beside) > 0 {','if false && len(beside) > 0 {')],
}
name=sys.argv[1]
for f,old,new in R[name]:
    s=open(f).read()
    assert old in s,(name,f,old)
    open(f,'w').write(s.replace(old,new,1))
print("applied",name)
