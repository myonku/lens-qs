package store

import (
	"io/fs"
	"testing"

	"github.com/myonku/lens-qs-api/migrations"
)

// 验证内嵌的迁移文件可被读取（防止 embed 路径写错）。
func TestMigrationsEmbedded(t *testing.T) {
	t.Log("embed 根目录内容：")
	_ = fs.WalkDir(migrations.FS, ".", func(p string, d fs.DirEntry, err error) error {
		t.Logf("  %s", p)
		return nil
	})

	for _, dir := range []string{"sqlite", "postgres"} {
		entries, err := fs.ReadDir(migrations.FS, dir)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", dir, err)
		}
		if len(entries) == 0 {
			t.Fatalf("%s 下没有文件", dir)
		}
		for _, e := range entries {
			raw, err := migrations.FS.ReadFile(dir + "/" + e.Name())
			if err != nil {
				t.Fatalf("读取 %s/%s 失败: %v", dir, e.Name(), err)
			}
			if len(raw) == 0 {
				t.Fatalf("%s/%s 内容为空", dir, e.Name())
			}
		}
	}
}
