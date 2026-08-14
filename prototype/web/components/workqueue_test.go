package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestWorkItemTreeAttachesSubtasksRegardlessOfListOrder(t *testing.T) {
	items := []WorkItemView{
		{ID: "child", ParentID: "parent", Title: "Child"},
		{ID: "unrelated", Title: "Unrelated"},
		{ID: "parent", Title: "Parent"},
		{ID: "grandchild", ParentID: "child", Title: "Grandchild"},
	}
	tree := WorkItemTree(items)
	if len(tree) != 2 {
		t.Fatalf("root count = %d, want 2", len(tree))
	}
	var parent *WorkItemNodeView
	for _, node := range tree {
		if node.Item.ID == "parent" {
			parent = node
		}
	}
	if parent == nil || len(parent.Children) != 1 || parent.Children[0].Item.ID != "child" {
		t.Fatalf("parent tree = %#v", parent)
	}
	if len(parent.Children[0].Children) != 1 || parent.Children[0].Children[0].Item.ID != "grandchild" {
		t.Fatalf("nested child tree = %#v", parent.Children[0].Children)
	}
}

func TestWorkItemTreeKeepsFilteredSubtaskWithParentContext(t *testing.T) {
	tree := WorkItemTree([]WorkItemView{{ID: "child", ParentID: "filtered-parent", ParentNumber: 12, Title: "Visible child"}})
	if len(tree) != 1 || !tree[0].Detached {
		t.Fatalf("filtered subtask tree = %#v", tree)
	}
	var output bytes.Buffer
	if err := WorkItemTreeNode(tree[0], 0, WorkFilterView{Status: "active", Kind: "all"}, "csrf").Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"work-item-subtask", "Subtask", "Parent #0012", "Visible child"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("detached subtask omitted %q: %s", expected, html)
		}
	}
}
