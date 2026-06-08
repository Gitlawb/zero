package zerogit

import "testing"

func TestParseDiffStat(t *testing.T) {
	cases := []struct {
		name string
		stat string
		want DiffStat
	}{
		{
			name: "both insertions and deletions",
			stat: " 3 files changed, 12 insertions(+), 4 deletions(-)",
			want: DiffStat{FilesChanged: 3, Insertions: 12, Deletions: 4},
		},
		{
			name: "single file singular nouns",
			stat: " 1 file changed, 1 insertion(+), 1 deletion(-)",
			want: DiffStat{FilesChanged: 1, Insertions: 1, Deletions: 1},
		},
		{
			name: "insertions only",
			stat: " 2 files changed, 7 insertions(+)",
			want: DiffStat{FilesChanged: 2, Insertions: 7, Deletions: 0},
		},
		{
			name: "deletions only",
			stat: " 1 file changed, 5 deletions(-)",
			want: DiffStat{FilesChanged: 1, Insertions: 0, Deletions: 5},
		},
		{
			name: "multi-line stat with trailing summary",
			stat: " a.go | 2 +-\n b.go | 3 ++-\n 2 files changed, 3 insertions(+), 2 deletions(-)",
			want: DiffStat{FilesChanged: 2, Insertions: 3, Deletions: 2},
		},
		{
			name: "empty",
			stat: "",
			want: DiffStat{},
		},
		{
			name: "malformed numbers do not panic",
			stat: " x files changed, y insertions(+), z deletions(-)",
			want: DiffStat{},
		},
		{
			name: "no summary line",
			stat: " a.go | 2 +-\n b.go | 3 ++-",
			want: DiffStat{},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ParseDiffStat(testCase.stat)
			if got != testCase.want {
				t.Fatalf("ParseDiffStat(%q) = %+v, want %+v", testCase.stat, got, testCase.want)
			}
		})
	}
}

func TestDiffStatNetLOC(t *testing.T) {
	stat := DiffStat{FilesChanged: 1, Insertions: 10, Deletions: 3}
	if got := stat.NetLOC(); got != 7 {
		t.Fatalf("NetLOC() = %d, want 7", got)
	}
}
