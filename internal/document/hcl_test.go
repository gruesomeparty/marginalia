package document

import (
	"strings"
	"testing"
)

const bucketTF = `# Where the logs go.
resource "aws_s3_bucket" "logs" {
  bucket = "acme-logs"

  versioning {
    enabled = true
  }
}

variable "region" {
  default = "eu-west-1"
}
`

// Issue #151: one block per HCL block, anchored by type and labels, what is
// inside it after a slash.
func TestHCLAnchorsByTypeAndLabels(t *testing.T) {
	doc, err := ParseBytes("main.tf", []byte(bucketTF))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != FormatHCL || !doc.IsTree() {
		t.Fatalf("format %q, tree %v", doc.Format, doc.IsTree())
	}
	want := []string{
		"resource.aws_s3_bucket.logs",
		"resource.aws_s3_bucket.logs/bucket",
		"resource.aws_s3_bucket.logs/versioning",
		"resource.aws_s3_bucket.logs/versioning/enabled",
		"variable.region",
		"variable.region/default",
	}
	if got := ids(doc); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	logs := doc.Blocks[0]
	if logs.Kind != "block" || !logs.HasChildren || !strings.HasPrefix(logs.Quote, "# Where the logs go.") {
		t.Errorf("block = %q quoting %q, children %v", logs.Kind, logs.Quote, logs.HasChildren)
	}
	if doc.Blocks[3].Level != 2 || doc.Blocks[3].Parent != "resource.aws_s3_bucket.logs/versioning" {
		t.Errorf("nested attribute at level %d under %q", doc.Blocks[3].Level, doc.Blocks[3].Parent)
	}
	if !strings.Contains(doc.Blocks[1].HTML, `class="tok str"`) {
		t.Errorf("attribute not highlighted: %s", doc.Blocks[1].HTML)
	}
	for _, ext := range []string{".tf", ".tfvars", ".hcl"} {
		if FormatFor("x"+ext) != FormatHCL {
			t.Errorf("%s is not HCL", ext)
		}
	}
}

// Ids and hashes are stable across an edit elsewhere in the file, and an
// edit to one attribute changes that attribute's hash only.
func TestHCLEditsElsewhereMoveNothing(t *testing.T) {
	before, _ := ParseBytes("main.tf", []byte(bucketTF))
	edited := strings.Replace(bucketTF, `default = "eu-west-1"`, `default = "us-east-1"`, 1)
	edited = "terraform {\n  required_version = \">= 1.6\"\n}\n\n" + edited
	after, _ := ParseBytes("main.tf", []byte(edited))
	now := map[string]string{}
	for _, b := range after.Blocks {
		now[b.ID] = b.Hash
	}
	for _, b := range before.Blocks {
		changed := now[b.ID] != b.Hash
		if want := b.ID == "variable.region/default"; changed != want {
			t.Errorf("%s: hash changed %v, want %v", b.ID, changed, want)
		}
	}
}

// A file that does not parse still renders what it can, and an empty one
// says so.
func TestHCLBrokenAndEmpty(t *testing.T) {
	doc, err := ParseBytes("main.tf", []byte("resource \"a\" \"b\" {\n  x = (1\n}\n???\nlocals {\n  y = 2\n"))
	if err != nil || len(doc.Blocks) == 0 {
		t.Fatalf("broken file: %v, %d blocks", err, len(doc.Blocks))
	}
	doc, _ = ParseBytes("empty.tf", []byte("\n"))
	if len(doc.Blocks) != 1 || doc.Blocks[0].ID != "@empty" {
		t.Errorf("empty file: %v", ids(doc))
	}
}
