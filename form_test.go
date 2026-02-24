package main

import (
	"testing"

	beadslite "github.com/kylesnowschwartz/beads-lite"
)

func TestNewFormDefaults(t *testing.T) {
	f := newForm(colTodo)

	if f.priority != 2 {
		t.Errorf("default priority = %d, want 2 (P2 medium)", f.priority)
	}
	if f.typeIndex != 0 {
		t.Errorf("default typeIndex = %d, want 0 (task)", f.typeIndex)
	}
	if f.focus != fieldTitle {
		t.Errorf("default focus = %d, want fieldTitle", f.focus)
	}
	if f.columnIndex != colTodo {
		t.Errorf("columnIndex = %d, want colTodo", f.columnIndex)
	}
	if f.description.Value() != "" {
		t.Errorf("default description = %q, want empty", f.description.Value())
	}
}

func TestEditFormPreservesFields(t *testing.T) {
	issue := beadslite.NewIssue("test card")
	issue.Priority = 1
	issue.Type = beadslite.IssueTypeFeature
	issue.Description = "some details"

	f := editForm(issue, colDoing)

	if f.title.Value() != "test card" {
		t.Errorf("title = %q, want %q", f.title.Value(), "test card")
	}
	if f.description.Value() != "some details" {
		t.Errorf("description = %q, want %q", f.description.Value(), "some details")
	}
	if f.priority != 1 {
		t.Errorf("priority = %d, want 1", f.priority)
	}
	if typeOptions[f.typeIndex] != beadslite.IssueTypeFeature {
		t.Errorf("type = %q, want %q", typeOptions[f.typeIndex], beadslite.IssueTypeFeature)
	}
}

func TestAdvanceFocusWraps(t *testing.T) {
	f := newForm(colTodo)

	// Forward through all fields: title -> description -> priority -> type -> title
	f.advanceFocus(1)
	if f.focus != fieldDescription {
		t.Errorf("after tab 1: focus = %d, want fieldDescription", f.focus)
	}
	f.advanceFocus(1)
	if f.focus != fieldPriority {
		t.Errorf("after tab 2: focus = %d, want fieldPriority", f.focus)
	}
	f.advanceFocus(1)
	if f.focus != fieldType {
		t.Errorf("after tab 3: focus = %d, want fieldType", f.focus)
	}
	f.advanceFocus(1)
	if f.focus != fieldTitle {
		t.Errorf("after tab 4: focus = %d, want fieldTitle (wrapped)", f.focus)
	}

	// Backward wraps too
	f.advanceFocus(-1)
	if f.focus != fieldType {
		t.Errorf("after shift-tab: focus = %d, want fieldType", f.focus)
	}
}

func TestAdvanceFocusBlursTextComponents(t *testing.T) {
	f := newForm(colTodo)

	// Title starts focused
	if !f.title.Focused() {
		t.Error("title should be focused initially")
	}

	// Tab to description: title blurred, description focused
	f.advanceFocus(1)
	if f.title.Focused() {
		t.Error("title should be blurred after advancing to description")
	}
	if !f.description.Focused() {
		t.Error("description should be focused")
	}

	// Tab to priority: both text components blurred
	f.advanceFocus(1)
	if f.title.Focused() || f.description.Focused() {
		t.Error("both text components should be blurred on selector field")
	}
}

func TestPriorityBounds(t *testing.T) {
	f := newForm(colTodo)
	f.priority = 0

	// Can't go below 0
	f.priority--
	if f.priority < 0 {
		f.priority = 0
	}
	if f.priority != 0 {
		t.Errorf("priority went below 0")
	}

	f.priority = 4
	// Can't go above 4
	f.priority++
	if f.priority > 4 {
		f.priority = 4
	}
	if f.priority != 4 {
		t.Errorf("priority went above 4")
	}
}

func TestTypeIndexWraps(t *testing.T) {
	f := newForm(colTodo)
	f.typeIndex = 0

	// Wrap backward
	f.typeIndex = (f.typeIndex - 1 + len(typeOptions)) % len(typeOptions)
	if f.typeIndex != len(typeOptions)-1 {
		t.Errorf("typeIndex backward wrap = %d, want %d", f.typeIndex, len(typeOptions)-1)
	}
	if typeOptions[f.typeIndex] != beadslite.IssueTypeEpic {
		t.Errorf("wrapped type = %q, want epic", typeOptions[f.typeIndex])
	}

	// Wrap forward
	f.typeIndex = (f.typeIndex + 1) % len(typeOptions)
	if f.typeIndex != 0 {
		t.Errorf("typeIndex forward wrap = %d, want 0", f.typeIndex)
	}
}
