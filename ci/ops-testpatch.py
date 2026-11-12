# test only: ops fills in DNSIP_PLACEHOLDER and IPOF_<service> in the nginx default.conf of a package
p = "/tmp/ops-src/cmd/compose.go"
s = open(p).read()
anchor = '\tp, ctx, err := getProviderAndContext(c, "onprem")\n'
assert anchor in s
s = s.replace(anchor, """\tif cf := filepath.Join(filepath.Dir(ppath), "sysroot/etc/nginx/conf.d/default.conf"); true {
		if b, e := os.ReadFile(cf); e == nil {
			t := strings.ReplaceAll(string(b), "DNSIP_PLACEHOLDER", dnsIP)
			for k, v := range testIPs {
				t = strings.ReplaceAll(t, "IPOF_"+k, v)
			}
			os.WriteFile(cf, []byte(t), 0644)
		}
	}
""" + anchor, 1)
old = "\t\terr = com.addDNS(dnsIP, y.Packages[i].Pkg, ip, non)\n"
assert old in s
s = s.replace(old, "\t\ttestIPs[y.Packages[i].Pkg] = ip\n" + old, 1)
s += "\nvar testIPs = map[string]string{}\n"
open(p, "w").write(s)
