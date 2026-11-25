import sys
T='internal/trace/trace.go'
R={
 'nearest-rule-off':[(T,'if program := nearestProgram(process); program != "" {','if program := nearestProgram(process); false && program != "" {')],
 'nearest-latest-wins-at-same-depth':[(T,'(depth == bestDepth && e.Line > last[best].Line)','(depth == bestDepth && e.Line < last[best].Line)')],
 'nearest-nearest-wins':[(T,'best == "" || depth < bestDepth ||','best == "" || depth > bestDepth ||')],
 'nearest-shells-are-not-the-program':[(T,'shells[interp] ||','(false && shells[interp]) ||')],
 'nearest-ps-is-not-the-program':[(T,'name == "ps" ||','')],
 'nearest-supervisors-are-not-the-program':[(T,' || Supervisor(e.Path, e.Argv) != "" {',' {')],
 'nearest-only-for-a-shell':[(T,'len(children) > 1 && shells[path.Base(last[process].Path)] && !launcher && !isInit {','len(children) > 1 && (shells[path.Base(last[process].Path)] || isInit) {')],
 'nearest-subshell-is-the-shell':[(T,'			for hops := 0; hops < 64 && !below; hops++ {\n				up := starter(x)','			for hops := 0; hops < 64 && !below; hops++ {\n				up := parent[x]')],
 'nearest-exited-are-not-candidates':[(T,'		for _, candidate := range running {\n			if candidate == shell {','		for _, candidate := range append(running, "9") {\n			if _, ok := last[candidate]; !ok || candidate == shell {')],
}
name=sys.argv[1]
for f,old,new in R[name]:
    s=open(f).read()
    assert old in s,(name,f,old)
    open(f,'w').write(s.replace(old,new,1))
print("applied",name)
