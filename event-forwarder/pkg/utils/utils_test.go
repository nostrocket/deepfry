package utils

import "testing"

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		input    uint64
		expected string
	}{
		{0, "0"},
		{123, "123"},
		{1234, "1,234"},
		{1234567, "1,234,567"},
	}

	for _, test := range tests {
		result := FormatNumber(test.input)
		if result != test.expected {
			t.Errorf("FormatNumber(%d) = %s; expected %s", test.input, result, test.expected)
		}
	}
}

func TestGetKindName(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "Metadata"},
		{1, "Text Note"},
		{999, "Kind 999"},
	}

	for _, test := range tests {
		result := GetKindName(test.input)
		if result != test.expected {
			t.Errorf("GetKindName(%d) = %s; expected %s", test.input, result, test.expected)
		}
	}
}

func TestSortEventKindsByCount(t *testing.T) {
	input := map[int]uint64{
		1: 100,
		7: 50,
		6: 200,
		3: 50,
	}

	result := SortEventKindsByCount(input)

	// Should be sorted by count descending: 6(200), 1(100), 7(50), 3(50)
	// For same count, sorted by kind ascending: 3 before 7
	expected := []KindCount{
		{Kind: 6, Count: 200},
		{Kind: 1, Count: 100},
		{Kind: 3, Count: 50},
		{Kind: 7, Count: 50},
	}

	if len(result) != len(expected) {
		t.Fatalf("Expected %d items, got %d", len(expected), len(result))
	}

	for i, exp := range expected {
		if result[i].Kind != exp.Kind || result[i].Count != exp.Count {
			t.Errorf("At index %d: expected %+v, got %+v", i, exp, result[i])
		}
	}
}
