import sys
T='internal/trace/trace.go'; O='internal/observe/observe.go'; G='internal/gen/gen.go'; L='internal/trace/launcher.go'
R={
 's6-prefix-for-the-program':[(L,r' || strings.HasPrefix(name, "s6-")','')],
 'runsv':[(L,r'"runsv": true, ','')],
 'gen-uses-the-prefix':[(G,r'trace.IsProcessSupervisor(name)','trace.ProcessSupervisors[name]')],
 'supervisor-uses-the-prefix':[(L,'if b := path.Base(n); IsProcessSupervisor(b) {','if b := path.Base(n); ProcessSupervisors[b] {')],
 'script-is-told-by-exact-names':[(L,'if b := path.Base(n); ProcessSupervisors[b] {','if b := path.Base(n); IsProcessSupervisor(b) {')],
 'soft-advice':[(G,'case trace.IsProcessSupervisor(name):','case false:')],
 'strong-advice-only-for-the-top':[(G,'case trace.StartsEveryService(name):','case trace.IsProcessSupervisor(name):')],
 'soft-advice-wording':[(G,' is taken for a tool of the s6 or runit suite, by its name:',' is a tool of a process supervision suite:')],
}
name=sys.argv[1]
for f,old,new in R[name]:
    s=open(f).read()
    assert old in s,(name,f,old)
    open(f,'w').write(s.replace(old,new,1))
print("applied",name)
