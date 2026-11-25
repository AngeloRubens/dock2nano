import sys
T='internal/trace/trace.go'; O='internal/observe/observe.go'; G='internal/gen/gen.go'; L='internal/trace/launcher.go'
R={
 's6-prefix-for-the-program':[(L,r' || strings.HasPrefix(name, "s6-")','')],
 'runsv':[(L,r'"runsv": true, ','')],
 'gen-uses-the-prefix':[(G,r'trace.IsProcessSupervisor(name)','trace.ProcessSupervisors[name]')],
 'supervisor-uses-the-prefix':[(L,r'IsProcessSupervisor(b) {
			return b
		}
	}
	for _, n := range scripts','ProcessSupervisors[b] {
			return b
		}
	}
	for _, n := range scripts')],
 'script-is-told-by-exact-names':[(L,r'if b := path.Base(n); ProcessSupervisors[b] {
			return b
		}
	}
}','if b := path.Base(n); IsProcessSupervisor(b) {
			return b
		}
	}
}')],
 'soft-advice':[(G,r'if trace.IsProcessSupervisor(name) && !trace.StartsEveryService(name) {','if false && trace.IsProcessSupervisor(name) && !trace.StartsEveryService(name) {')],
 'strong-advice-only-for-the-top':[(G,r'if trace.StartsEveryService(name) {','if trace.IsProcessSupervisor(name) {')],
}
name=sys.argv[1]
for f,old,new in R[name]:
    s=open(f).read()
    assert old in s,(name,f,old)
    open(f,'w').write(s.replace(old,new,1))
print("applied",name)
