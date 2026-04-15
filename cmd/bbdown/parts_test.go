package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestParsePartSpec(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		total   int
		want    []int
		wantErr bool
	}{
		{name: "single", spec: "1", total: 5, want: []int{1}},
		{name: "list", spec: "1,2,3", total: 5, want: []int{1, 2, 3}},
		{name: "range", spec: "3-5", total: 5, want: []int{3, 4, 5}},
		{name: "mixed", spec: "1,3-5,LAST", total: 5, want: []int{1, 3, 4, 5}},
		{name: "mixed_with_last_gap", spec: "1,3-4,LAST", total: 7, want: []int{1, 3, 4, 7}},
		{name: "ALL", spec: "ALL", total: 3, want: []int{1, 2, 3}},
		{name: "all_lowercase", spec: "all", total: 2, want: []int{1, 2}},
		{name: "LAST", spec: "LAST", total: 4, want: []int{4}},
		{name: "last_lowercase", spec: "last", total: 4, want: []int{4}},
		{name: "dedup", spec: "1,1,2,2-3", total: 5, want: []int{1, 2, 3}},
		{name: "whitespace", spec: " 1 , 2-3 ", total: 5, want: []int{1, 2, 3}},
		{name: "range_to_LAST", spec: "2-LAST", total: 4, want: []int{2, 3, 4}},

		{name: "empty", spec: "", total: 5, wantErr: true},
		{name: "whitespace_only", spec: "   ", total: 5, wantErr: true},
		{name: "non_numeric", spec: "abc", total: 5, wantErr: true},
		{name: "inverted_range", spec: "5-3", total: 10, wantErr: true},
		{name: "range_no_total", spec: "1-LAST", total: 0, wantErr: true},
		{name: "ALL_no_total", spec: "ALL", total: 0, wantErr: true},
		{name: "LAST_no_total", spec: "LAST", total: 0, wantErr: true},
		{name: "zero_page", spec: "0", total: 5, wantErr: true},
		{name: "negative_page", spec: "-1", total: 5, wantErr: true},
		{name: "exceeds_total", spec: "10", total: 5, wantErr: true},
		{name: "range_partial_junk", spec: "1-x", total: 5, wantErr: true},
		{name: "ALL_not_alone", spec: "ALL,1", total: 5, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePartSpec(tt.spec, tt.total)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}
				if !errors.Is(err, ErrPartSpec) {
					t.Fatalf("want ErrPartSpec, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
