package taskqueue

import "testing"

func TestDefaultListOptionsAndNormalize(t *testing.T) {
	opts := DefaultListOptions()
	if opts.Page != 1 {
		t.Fatalf("DefaultListOptions().Page = %d, want 1", opts.Page)
	}
	if opts.PageSize != 30 {
		t.Fatalf("DefaultListOptions().PageSize = %d, want 30", opts.PageSize)
	}

	cases := []struct {
		name string
		opts ListOptions
		want ListOptions
	}{
		{
			name: "negative values use defaults",
			opts: ListOptions{Page: -1, PageSize: -5},
			want: ListOptions{Page: 1, PageSize: 30},
		},
		{
			name: "page is clamped to minimum",
			opts: ListOptions{Page: 0, PageSize: 10},
			want: ListOptions{Page: 1, PageSize: 10},
		},
		{
			name: "page size is clamped to max",
			opts: ListOptions{Page: 2, PageSize: 500},
			want: ListOptions{Page: 2, PageSize: 100},
		},
		{
			name: "valid values are preserved",
			opts: ListOptions{Page: 3, PageSize: 50},
			want: ListOptions{Page: 3, PageSize: 50},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.opts
			got.Normalize()
			if got != tc.want {
				t.Fatalf("Normalize() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
