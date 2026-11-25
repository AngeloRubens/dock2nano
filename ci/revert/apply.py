import sys
T='internal/trace/trace.go'; O='internal/observe/observe.go'; G='internal/gen/gen.go'; L='internal/trace/launcher.go'
R={
 'nearest-rule-off':[(T,r'if program, others := nearestProgram(process); program != "" {','if program, others := nearestProgram(process); false && program != "" {')],
 'nearest-fork-order-decides':[(T,r'case c.branchAt != b.branchAt:','case false:')],
 'nearest-same-branch-later-execve':[(T,r'case last[c.pid].Line > last[b.pid].Line:','case last[c.pid].Line < last[b.pid].Line:')],
 'nearest-nearer-wins':[(T,r'if c.depth < b.depth {','if c.depth > b.depth {')],
 'nearest-call-start-line':[(T,r'if start, ok := callStart[m[1]+m[2]]; ok {','if start, ok := callStart[m[1]+m[2]]; ok && false {')],
 'nearest-shells-are-not-the-program':[(T,r'c.notProgram = c.isShell || ','c.notProgram = ')],
 'nearest-busybox-is-not-the-program':[(T,r'name == "busybox" || ','')],
 'nearest-ps-is-not-the-program':[(T,r'name == "ps" || ','')],
 'nearest-sleep-is-not-the-program':[(T,r'name == "sleep" || ','')],
 'nearest-tail-is-not-the-program':[(T,r'name == "tail" || ','')],
 'nearest-supervisors-are-not-the-program':[(T,r' || Supervisor(e.Path, e.Argv) != ""','')],
 'nearest-exited-are-not-candidates':[(T,'for _, pid := range running {\n			if pid == shell {','for _, pid := range all {\n			if pid == shell {')],
 'nearest-only-for-a-shell':[(T,r'len(children) > 1 && shells[path.Base(last[process].Path)] && !launcher && !isInit {','len(children) > 1 && (shells[path.Base(last[process].Path)] || isInit) {')],
 'nearest-subshell-is-the-shell':[(T,'				up := starter(x)\n				if up == "" {\n					break\n				}\n				c.depth++','				up := parent[x]\n				if up == "" {\n					break\n				}\n				c.depth++')],
 'beside-recorded':[(T,r'beside = append(beside, Label(last[o].Path, last[o].Argv))','_ = o')],
 'beside-leaves-out-what-is-below-the-program':[(T,r'k == best || c.isShell || slices.Contains(c.chain, chosen)','k == best || c.isShell')],
 'beside-leaves-out-shells':[(T,r'k == best || c.isShell ||','k == best ||')],
 'beside-handed-on':[(T,r'	final.Beside = beside','')],
 'beside-carried-by-observe':[(O,r'		Beside:             final.Beside,','')],
 'beside-item-in-the-report':[(G,r'if len(obs.Beside) > 0 {','if false && len(obs.Beside) > 0 {')],
}
name=sys.argv[1]
for f,old,new in R[name]:
    s=open(f).read()
    assert old in s,(name,f,old)
    open(f,'w').write(s.replace(old,new,1))
print("applied",name)
