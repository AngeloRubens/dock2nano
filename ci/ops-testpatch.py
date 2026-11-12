p = "/tmp/ops-src/cmd/compose.go"
s = open(p).read()
s = s.replace("\tc.NameServers = []string{dnsIP}\n", """\tc.NameServers = []string{dnsIP}
	if cf := filepath.Join(filepath.Dir(ppath), "sysroot/etc/nginx/conf.d/default.conf"); true {
		if b, e := os.ReadFile(cf); e == nil {
			t := strings.ReplaceAll(string(b), "DNSIP_PLACEHOLDER", dnsIP)
			for k, v := range testIPs {
				t = strings.ReplaceAll(t, "IPOF_"+k, v)
			}
			os.WriteFile(cf, []byte(t), 0644)
		}
	}
""", 1)
s = s.replace("\t\terr = com.addDNS(dnsIP, y.Packages[i].Pkg, ip, non)\n", "\t\ttestIPs[y.Packages[i].Pkg] = ip\n\t\terr = com.addDNS(dnsIP, y.Packages[i].Pkg, ip, non)\n", 1)
s += "\nvar testIPs = map[string]string{}\n"
open(p, "w").write(s)
