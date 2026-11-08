import subprocess, sys

# One mutation of cmd/compose.go per job; the tests of compose have to fail (KILLED).
M = {
    # a record is added although the address did not turn up
    "empty-record-added": (
        '''	if err != nil {
		return fmt.Errorf("%s: %w: its name is not registered in the dns", host, err)
	}
''', '''	if err != nil {
		com.addDNS(dnsIP, host, ip, non)
		return fmt.Errorf("%s: %w: its name is not registered in the dns", host, err)
	}
'''),
    # ten seconds again
    "wait-ten-seconds": ("const waitForIPTries = 60", "const waitForIPTries = 20"),
    # addDNS does not return when the request cannot be made at all
    "adddns-ignores-errors": (
        '''	res, err := client.Do(req)
	if err != nil {
		return err
	}''', '''	res, err := client.Do(req)
	if err != nil {
		fmt.Println(err)
	}'''),
    "adddns-ignores-do-error": (
        '''	req, err := http.NewRequest("GET", "http://"+dnsIP+":"+dnsPort+"/add?svc="+host+".service&ip="+ip, nil)
	if err != nil {
		return err
	}
''', '''	req, _ := http.NewRequest("GET", "http://"+dnsIP+":"+dnsPort+"/add?svc="+host+".service&ip="+ip, nil)
'''),
    # UP goes on without the address of the dns: not covered by a test (UP starts qemu), the job
    # says SURVIVED for it and that is the finding
    "dns-without-address-goes-on": (
        '''		fmt.Println(err)
		return
	}
''', '''		fmt.Println(err)
	}
'''),
}

name = sys.argv[1]
old, new = M[name]
path = "cmd/compose.go"
s = open(path).read()
if s.count(old) != 1:
    sys.exit("%s: pattern found %d times" % (name, s.count(old)))
open(path, "w").write(s.replace(old, new))
r = subprocess.run(["go", "test", "./cmd/...", "-count", "1", "-timeout", "120s", "-run", "RegisterService|AddDNS"],
                   capture_output=True, text=True)
out = r.stdout + r.stderr
print(out[-2500:])
if r.returncode == 0:
    sys.exit("SURVIVED: %s" % name)
if "[build failed]" in out or "setup failed" in out:
    sys.exit("BROKEN MUTATION: %s" % name)
print("KILLED: %s" % name)
