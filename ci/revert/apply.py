import sys
T='internal/trace/trace.go'; O='internal/observe/observe.go'; G='internal/gen/gen.go'; L='internal/trace/launcher.go'
R={
 'hostname-from-env':[(O,r'ok && hostnameOK.MatchString(v) {','ok && false && hostnameOK.MatchString(v) {')],
 'hostname-must-be-valid':[(O,r'ok && hostnameOK.MatchString(v) {','ok {')],
 'hosts-line-names-the-hostname':[(O,r'localhost " + hostname + "\n::1',r'localhost " + "" + "\n::1')],
 'hostname-file':[(O,r'if err := os.WriteFile(filepath.Join(sysroot, "etc", "hostname"), []byte(hostname+"\n"), 0o644); err != nil {','if err := error(nil); err != nil {')],
 'report-names-the-hostname':[(G,r'if obs.Hostname != "" {','if false {')],
}
name=sys.argv[1]
for f,old,new in R[name]:
    s=open(f).read()
    assert old in s,(name,f,old)
    open(f,'w').write(s.replace(old,new,1))
print("applied",name)
