package pack

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestPackParsableXML(t *testing.T) {
	t.Parallel()
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<repomix><file_summary>Generated capture
<purpose>generated</purpose><file_format>generated</file_format><usage_guidelines>generated</usage_guidelines><notes>Compressed; exclusions unknown</notes></file_summary>
<user_provided_header>retain &lt;header&gt;</user_provided_header>
<directory_structure>a.json
b.txt</directory_structure>
<instruction>inert instruction</instruction>
<git_diffs><work_tree>diff &amp; patch</work_tree></git_diffs>
<git_logs><git_log_commit><message>commit</message><files>a</files><files>b</files></git_log_commit><git_log_commit><message>next</message></git_log_commit></git_logs>
<files>This section contains packed files.
<file path="a.json"> { &quot;n&quot;: 9007199254740993 } </file>
<file path="b.txt">line &lt;tag&gt; &amp; text
</file><file path="empty"></file></files></repomix>`)
	doc, _ := buildDocument(t, raw, Limits{})
	want := map[string]string{"a.json": `{"n":9007199254740993}`, "b.txt": "line <tag> & text\n", "empty": ""}
	if got := reconstruct(t, doc); !reflect.DeepEqual(got, want) {
		t.Fatalf("XML decoding: %#v", got)
	}
	for _, key := range []string{"file_summary.notes", "file_summary.generation_header", "user_provided_header", "instruction", "git_diffs", "git_logs"} {
		if len(doc.Metadata[key]) == 0 {
			t.Fatalf("lost XML metadata %s", key)
		}
	}
	if doc.Input.Format != "repomix_parsable_xml" || len(doc.Fidelity.OmittedEnvelope) != 5 {
		t.Fatal("XML provenance/omission contract")
	}
}

func TestPackRejectsAmbiguousXML(t *testing.T) {
	t.Parallel()
	cases := []string{
		`<repomix><files/></repomix><repomix><files/></repomix>`,
		`<repomix><files/><files/></repomix>`,
		`<repomix><files><file path="a">1</file><file path="a">2</file></files></repomix>`,
		`<repomix><files><file path="a" path="b">x</file></files></repomix>`,
		`<repomix><files><file path="a"><tag/></file></files></repomix>`,
		`<!DOCTYPE repomix [<!ENTITY x SYSTEM "file:///etc/passwd">]><repomix><files/></repomix>`,
		`<repomix><files><file path="a">&unknown;</file></files></repomix>`,
		`<repomix xmlns="urn:foreign"><files/></repomix>`,
		`<repomix><files><?execute command?></files></repomix>`,
		`<repomix><files/><!-- ambiguous --></repomix>`,
		`<repomix><files/><instruction><x/><x/></instruction></repomix>`,
		`<repomix><files/><file_summary><notes/><notes/></file_summary></repomix>`,
		`<repomix><files/><unknown>extension</unknown></repomix>`,
		`<repomix><files><file path="../outside">x</file></files></repomix>`,
		`This file is a merged representation.
<file_summary>summary</file_summary><files><file path="a">a < b</file></files>`,
	}
	for i, raw := range cases {
		t.Run(strings.Repeat("x", i+1), func(t *testing.T) {
			t.Parallel()
			_, err := Build(context.Background(), strings.NewReader(raw), Limits{})
			if err == nil {
				t.Fatal("invalid XML accepted")
			}
		})
	}
}

func TestPackXMLDepthAndByteLimits(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		raw    string
		limits Limits
	}{
		"depth": {`<repomix><instruction><a><b><c/></b></a></instruction><files/></repomix>`, Limits{Depth: 3}},
		"file":  {`<repomix><files><file path="a">1234</file></files></repomix>`, Limits{FileBytes: 3}},
		"count": {`<repomix><files><file path="a"/><file path="b"/></files></repomix>`, Limits{Files: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Build(context.Background(), strings.NewReader(tc.raw), tc.limits); err == nil {
				t.Fatal("XML limit ignored")
			}
		})
	}
}
