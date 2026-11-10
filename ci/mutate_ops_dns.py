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
    "adddns-ignores-newrequest-error": (
        '''	req, err := http.NewRequest("GET", "http://"+dnsIP+":"+dnsPort+"/add?svc="+url.QueryEscape(host+".service")+"&ip="+url.QueryEscape(ip), nil)
	if err != nil {
		return err
	}
''', '''	req, _ := http.NewRequest("GET", "http://"+dnsIP+":"+dnsPort+"/add?svc="+url.QueryEscape(host+".service")+"&ip="+url.QueryEscape(ip), nil)
'''),
    # an error status of the dns counts as success
    "adddns-ignores-status": (
        "if res.StatusCode != http.StatusOK {", "if false && res.StatusCode != http.StatusOK {"),
    # the client waits for ever for a dns that does not answer
    "adddns-no-timeout": ("return &http.Client{Timeout: dnsTimeout}", "return &http.Client{}"),
    # no sleep-skip is not tested; the service that failed does not make the up fail
    "failed-service-is-not-an-error": (
        '''		exitWithError("not registered in the dns: " + strings.Join(failed, ", "))''',
        '''		fmt.Println("not registered in the dns: " + strings.Join(failed, ", "))'''),
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
