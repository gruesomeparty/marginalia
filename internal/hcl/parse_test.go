package hcl

import (
	"fmt"
	"strings"
	"testing"
)

// outline renders nodes as `kind type labels` lines, indented by depth.
func outline(nodes []*Node, depth int, b *strings.Builder) {
	for _, n := range nodes {
		fmt.Fprintf(b, "%s%s %s", strings.Repeat("  ", depth), n.Kind, n.Type)
		for _, l := range n.Labels {
			fmt.Fprintf(b, " %q", l)
		}
		b.WriteString("\n")
		outline(n.Children, depth+1, b)
	}
}

func show(nodes []*Node) string {
	var b strings.Builder
	outline(nodes, 0, &b)
	return b.String()
}

const mainTF = `# Where the logs go.
resource "aws_s3_bucket" "logs" {
  bucket = "acme-logs" # renamed in Q3
  tags = {
    Team = "infra"
  }

  versioning {
    enabled = true
  }
}

variable "region" { default = "eu-west-1" }

locals {
  policy = <<-EOT
    { "Statement": [ { "Effect": "Allow" } ] }
  EOT
  name   = "${var.prefix}-${jsonencode({ a = "}" })}"
}
`

func TestParseStructure(t *testing.T) {
	nodes := Parse([]byte(mainTF))
	want := `block resource "aws_s3_bucket" "logs"
  attribute bucket
  attribute tags
  block versioning
    attribute enabled
block variable "region"
  attribute default
block locals
  attribute policy
  attribute name
`
	if got := show(nodes); got != want {
		t.Fatalf("outline:\n%s\nwant:\n%s", got, want)
	}
	logs := nodes[0]
	if logs.Comment != "# Where the logs go." || logs.Text != `resource "aws_s3_bucket" "logs" {` {
		t.Errorf("block header %q, comment %q", logs.Text, logs.Comment)
	}
	if b := logs.Children[0]; b.Text != `bucket = "acme-logs"` || b.Trailing != "# renamed in Q3" {
		t.Errorf("attribute %q trailing %q", b.Text, b.Trailing)
	}
	if tags := logs.Children[1]; !strings.HasSuffix(tags.Text, "}") || !strings.Contains(tags.Text, "Team") {
		t.Errorf("multi-line attribute cut short: %q", tags.Text)
	}
	if !strings.HasSuffix(strings.TrimSpace(mainTF[logs.Start:logs.End]), "}") {
		t.Errorf("block range %q does not end at its brace", mainTF[logs.Start:logs.End])
	}
}

// What cannot be read is kept, and does not take the rest of the file with it.
func TestParseTolerant(t *testing.T) {
	cases := map[string]string{
		"garbage line": "foo(1)\nresource \"a\" \"b\" {\n  x = 1\n}\n",
		"stray closer": "}\nresource \"a\" \"b\" {\n  x = 1\n}\n",
		"stray quote":  "x = \"oops\nresource \"a\" \"b\" {\n  x = 1\n}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			nodes := Parse([]byte(src))
			if len(nodes) != 2 || nodes[1].Kind != KindBlock || len(nodes[1].Children) != 1 {
				t.Fatalf("got:\n%s", show(nodes))
			}
		})
	}
	// An unclosed block runs to the end of the file rather than vanishing.
	nodes := Parse([]byte("resource \"a\" \"b\" {\n  x = 1\n"))
	if len(nodes) != 1 || len(nodes[0].Children) != 1 {
		t.Fatalf("unclosed block:\n%s", show(nodes))
	}
	// An unterminated heredoc is one attribute, not a hang.
	nodes = Parse([]byte("x = <<EOT\nnever ends\n"))
	if len(nodes) != 1 || nodes[0].Kind != KindAttribute {
		t.Fatalf("heredoc:\n%s", show(nodes))
	}
}

// A comment at the end of a body is kept as a node of its own, and one after
// a block's brace belongs to the block, not to its first attribute.
func TestParseComments(t *testing.T) {
	nodes := Parse([]byte("a { # the a block\n  x = 1\n  // closing remark\n}\n/* tail */\n"))
	if got := show(nodes); got != "block a\n  attribute x\n  comment \ncomment \n" {
		t.Fatalf("outline:\n%s", got)
	}
	if nodes[0].Trailing != "# the a block" || nodes[0].Children[0].Comment != "" {
		t.Errorf("trailing %q, first child's comment %q", nodes[0].Trailing, nodes[0].Children[0].Comment)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(mainTF)
	f.Add("a = \"${\"")
	f.Add("x = <<")
	f.Fuzz(func(t *testing.T, src string) {
		var walk func([]*Node, int)
		walk = func(nodes []*Node, from int) {
			for _, n := range nodes {
				if n.Start < from || n.End < n.Start || n.End > len(src) {
					t.Fatalf("node %q at [%d,%d) after %d in %d bytes", n.Text, n.Start, n.End, from, len(src))
				}
				walk(n.Children, n.Start)
				from = n.End
			}
		}
		walk(Parse([]byte(src)), 0)
	})
}
