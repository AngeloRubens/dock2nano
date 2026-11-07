import subprocess, sys

T = "\t"
M = {
 "merge-cap": ("const maxMergeSteps = 100000", "const maxMergeSteps = 1000000000", "TestMergeBombIsBounded|TestWideMergeIsBounded"),
 "merge-first-wins": ("\t\t\tfor _, s := range srcs {", "\t\t\tfor si := len(srcs) - 1; si >= 0; si-- {\n\t\t\t\ts := srcs[si]", "TestMergeSequenceFirstWins"),
 "merge-explicit-wins": ("\tfor _, e := range merged {\n\t\tif !seen[e.key] {", "\tfor _, e := range merged {\n\t\tif true {", "TestAnchorsAndMerge|TestExplicitKeyBeforeMergeWins"),
 "healthcheck-unknown": ("\t\tdefault:\n\t\t\tif !isExtension(e.key) {\n\t\t\t\ts.Unknown = append(s.Unknown, \"healthcheck.\"+e.key)\n\t\t\t}\n", "", "TestHealthcheckUnknownKeys|TestParseTwoServices"),
 "envfile-unknown": ("case !isExtension(e.key):\n\t\t\t\t\ts.Unknown = append(s.Unknown, fmt.Sprintf(\"env_file", "case false:\n\t\t\t\t\ts.Unknown = append(s.Unknown, fmt.Sprintf(\"env_file", "TestEnvFileLongForm"),
 "boolean-forms": ("case \"true\", \"yes\", \"on\":", "case \"true\":", "TestBooleanForms"),
 "command-set": ("func (c Command) IsSet() bool { return c.Set }", "func (c Command) IsSet() bool { return c.Shell != \"\" || c.Exec != nil }", "TestEmptyCommandIsSetAndNullIsNot"),
 "replicas-zero": ("\ts.Replicas = &c\n", "\tif c != 0 {\n\t\ts.Replicas = &c\n\t}\n", "TestReplicasZeroIsKept"),
 "replicas-disagree": ("*s.Replicas != c", "false", "TestErrors"),
 "toplevel-config": ("*unknown = append(*unknown, prefix+\".\"+e.key+\".\"+se.key)", "_ = unknown", "TestTopLevelNetworkVolumeConfigListed"),
 "unresolved-volume": ("strings.Contains(out.Source, \"$\")", "false", "TestShortVolumes|TestUnresolvedVolumeIsListed"),
 "dot-bind": ("\"./~\"", "\"/~\"", "TestShortVolumes"),
 "windows-drive": ("if v[2] == '\\\\' {", "if false {", "TestShortVolumes"),
 "volume-modes": ("return out, fmt.Errorf(\"volume %q has the mode %q\", v, m)", "opts = append(opts, m)", "TestShortVolumes"),
 "null-values": ("if modeled[e.key] && isNull(e.val) {", "if false {", "TestNullValuesAreEmpty"),
 "multi-document": ("if err := dec.Decode(&next); err != io.EOF {", "if err := dec.Decode(&next); false && err != io.EOF {", "TestErrors"),
 "reset-tag": (" || n.Tag == \"!reset\"", "", "TestResetTagIsNull"),
 "port-protocol": ("return p == \"tcp\" || p == \"udp\" || p == \"sctp\"", "return true", "TestShortPorts|TestLongPortProtocolIsChecked"),
 "port-double-slash": ("if strings.Contains(rest, \"/\") {", "if false {", "TestShortPorts"),
 "ipv6-colon": ("!strings.HasPrefix(rest[j+1:], \":\")", "false", "TestShortPorts"),
 "user-field": ("\tcase \"user\":", "\tcase \"user_\":", "TestPlainFields"),
 "build-unknown": ("s.Unknown = append(s.Unknown, \"build.\"+e.key)", "_ = s", "TestBuildUnknownAndEmptyVarName"),
 "empty-var-name": ("if name == \"\" {", "if false {", "TestBuildUnknownAndEmptyVarName"),
 "merge-entry-budget": ("\t\t\t\t*steps += len(es)\n", "", "TestWideMergeIsBounded"),
 "size-cap": ("const maxExpandedNodes = 1000000", "const maxExpandedNodes = 100000000000", "TestAliasAmplificationIsRefused"),
 "x-env-note": ("if isExtension(e.key) {\n\t\t\t\ts.Unknown = append(s.Unknown, label+", "if false {\n\t\t\t\ts.Unknown = append(s.Unknown, label+", "TestNamesStartingWithX"),
 "x-services": ("\tfor _, e := range es {\n\t\ts, err := parseService(e.key, e.val)", "\tfor _, e := range es {\n\t\tif isExtension(e.key) {\n\t\t\tcontinue\n\t\t}\n\t\ts, err := parseService(e.key, e.val)", "TestXNamesUnderServicesAndTopLevel|TestNamesStartingWithX"),
 "x-toplevel": ("\tfor _, e := range es {\n\t\tout = append(out, e.key)\n\t\tif isNull(e.val) {", "\tfor _, e := range es {\n\t\tif isExtension(e.key) {\n\t\t\tcontinue\n\t\t}\n\t\tout = append(out, e.key)\n\t\tif isNull(e.val) {", "TestXNamesUnderServicesAndTopLevel"),
 "split-vars": ("case s[i] == '$' && i+1 < len(s) && s[i+1] == '{':", "case false:", "TestInterpolationWithDefaultInShortSyntax"),
 "count-variable": ("\tif strings.Contains(v, \"$\") {\n\t\treturn 0, false, nil\n\t}\n\tc, err", "\tif false {\n\t\treturn 0, false, nil\n\t}\n\tc, err", "TestVariablesInCountsAndBooleans"),
 "bool-variable": ("\tif strings.Contains(v, \"$\") {\n\t\treturn false, false, nil\n\t}", "\tif false {\n\t\treturn false, false, nil\n\t}", "TestVariablesInCountsAndBooleans"),
 "scale-variable-note": ("s.Unknown = append(s.Unknown, label+\" (holds a variable)\")", "_ = label", "TestVariablesInCountsAndBooleans"),
 "null-list-item": ("if isNull(c) {\n\t\t\treturn nil, errAt(deref(c), \"empty entry in the list\")", "if false {\n\t\t\treturn nil, errAt(deref(c), \"empty entry in the list\")", "TestNullEntriesInLists"),
 "null-port-item": ("if isNull(c) {\n\t\t\treturn nil, errAt(c, \"empty entry in ports\")", "if false {\n\t\t\treturn nil, errAt(c, \"empty entry in ports\")", "TestNullEntriesInLists"),
 "null-volume-item": ("if isNull(c) {\n\t\t\treturn nil, errAt(c, \"empty entry in volumes\")", "if false {\n\t\t\treturn nil, errAt(c, \"empty entry in volumes\")", "TestNullEntriesInLists"),
 "null-port-field": ("if isNull(e.val) && (e.key == \"published\"", "if false && (e.key == \"published\"", "TestNestedNulls"),
 "null-condition": ("if isNull(se.val) {\n\t\t\t\t\t\tbreak", "if false {\n\t\t\t\t\t\tbreak", "TestNestedNulls"),
 "null-replicas": ("case e.key == \"replicas\" && isNull(e.val):", "case false:", "TestNestedNulls"),
 "null-args": ("if !isNull(e.val) {\n\t\t\t\tb.Args", "if true {\n\t\t\t\tb.Args", "TestNestedNulls"),
 "null-bool": ("case \"false\", \"no\", \"off\", \"\":", "case \"false\", \"no\", \"off\":", "TestNestedNulls"),
 "volume-type-required": ("return nil, errAt(c, \"volume has no type\")", "v.Type = \"volume\"", "TestLongVolumeTypes"),
 "volume-type-unknown": ("s.Unknown = append(s.Unknown, fmt.Sprintf(\"volumes[%d].type (%s)\", i, v.Type))", "_ = i", "TestLongVolumeTypes"),
 "long-port-protocol": ("if !validProtocol(p.Protocol) {\n\t\t\treturn nil, errAt(c, \"port has", "if false {\n\t\t\treturn nil, errAt(c, \"port has", "TestLongPortProtocolIsChecked"),
 "healthcheck-none": ("h.Test.Exec[0] == \"NONE\"", "h.Test.Exec[0] == \"NONEX\"", "TestHealthcheckNone"),
 "windows-slash-drive": ("strings.HasPrefix(more[1], \"/\")", "false", "TestShortVolumes"),
 "bind-before-variable": ("\tcase strings.ContainsAny(out.Source[:1], \"./~\") || drive != \"\":\n\t\tout.Type = \"bind\"\n\tcase strings.Contains(out.Source, \"$\"):\n\t\tout.Type = \"unresolved\"", "\tcase strings.Contains(out.Source, \"$\"):\n\t\tout.Type = \"unresolved\"\n\tcase strings.ContainsAny(out.Source[:1], \"./~\") || drive != \"\":\n\t\tout.Type = \"bind\"", "TestShortVolumes"),
 "cycle-refused": ("\t\t\tif cycleAt == nil {\n\t\t\t\tcycleAt = n\n\t\t\t}\n", "", "TestRecursiveAliasIsRefused|TestCycleDoesNotHideAmplification"),
 "merge-twice": ("\t\t\tif hasMerge {", "\t\t\tif false {", "TestTwoMergeKeysAreRefused"),
 "merge-tag-value": (" && k.Value == \"<<\"", "", "TestMergeTagWithOtherValue"),
 "env-file-null-item": ("if isNull(c) {\n\t\t\treturn nil, errAt(deref(c), \"empty entry in env_file\")", "if false {\n\t\t\treturn nil, errAt(deref(c), \"empty entry in env_file\")", "TestEnvFileNullEntries"),
 "env-file-empty-path": ("if p == \"\" {\n\t\t\t\t\t\treturn nil, errAt(deref(e.val), \"env_file path is empty\")", "if false {\n\t\t\t\t\t\treturn nil, errAt(deref(e.val), \"env_file path is empty\")", "TestEnvFileNullEntries"),
 "count-base0": ("ParseInt(strings.ReplaceAll(v, \"_\", \"\"), 0, 32)", "ParseInt(v, 10, 32)", "TestCountBases"),
 "unclosed-port-var": ("if openVar(v) {\n\t\treturn p, fmt.Errorf(\"port", "if false {\n\t\treturn p, fmt.Errorf(\"port", "TestUnclosedVariableInShortSyntax"),
 "unclosed-volume-var": ("if openVar(v) {\n\t\treturn Volume{}", "if false {\n\t\treturn Volume{}", "TestUnclosedVariableInShortSyntax"),
 "reset-env": ("\t\t\tif isReset(e.val) {\n\t\t\t\tcontinue // !reset removes the variable\n\t\t\t}\n", "", "TestResetRemovesEnvironmentVariable"),
}

def main():
    if sys.argv[1] == "list":
        print(" ".join(M))
        return
    name = sys.argv[1]
    old, new, tests = M[name]
    hit = 0
    for f in ("internal/compose/compose.go", "internal/compose/node.go"):
        s = open(f).read()
        if s.count(old) == 1:
            open(f, "w").write(s.replace(old, new))
            hit += 1
        elif s.count(old) > 1:
            sys.exit("%s: pattern found more than once in %s" % (name, f))
    if hit != 1:
        sys.exit("%s: pattern found in %d files" % (name, hit))
    r = subprocess.run(["go", "test", "-timeout", "90s", "-run", tests, "./internal/compose"],
                       capture_output=True, text=True)
    out = r.stdout + r.stderr
    print(out[-3000:])
    if r.returncode == 0:
        sys.exit("SURVIVED: %s: the tests still pass with the change reverted" % name)
    if "[build failed]" in out or "setup failed" in out:
        sys.exit("BROKEN MUTATION: %s does not compile" % name)
    print("KILLED: %s" % name)

main()
