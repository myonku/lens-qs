// Command seed 初始化基础数据：字典项与参考部件号清单。
//
// 用法：
//
//	go run ./cmd/seed                 # 字典 + 参考部件号（仅填补缺项，不覆盖人工资料）
//	go run ./cmd/seed -drawings-only  # 只导入参考部件号
//
// 说明：参考部件号清单来自业务方提供的图纸清单（序号 05~22）。
// 它**仅供参考**：现场输入与清单不一致属于正常情况，系统不会因此拒绝保存（需求 B-6）。
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"log"
	"os"
	"strings"

	"github.com/myonku/lens-qs-api/internal/dictionary"
	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"github.com/myonku/lens-qs-api/internal/store"
)

const drawingsFile = "data/reference_drawings.csv"

func main() {
	drawingsOnly := flag.Bool("drawings-only", false, "只导入参考部件号")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[fatal] 配置错误: %v", err)
	}
	db, err := database.Open(cfg.DBDriver, cfg.DBDSN, cfg.DBMaxOpen, cfg.DBMaxIdle)
	if err != nil {
		log.Fatalf("[fatal] 数据库不可用: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := store.CheckReady(ctx, db, cfg.DBDriver); err != nil {
		log.Fatal(err)
	}

	dictStore := dictionary.NewStore(db, cfg.DBDriver)

	if !*drawingsOnly {
		if err := seedDict(ctx, dictStore); err != nil {
			log.Fatalf("[fatal] 写入字典失败: %v", err)
		}
	}
	if err := seedDrawings(ctx, dictStore); err != nil {
		log.Fatalf("[fatal] 写入参考部件号失败: %v", err)
	}
	log.Println("[info] 初始化数据完成")
}

// seedDict 写入初始字典。取值来自需求文档与表单照片，均**可后续维护**。
func seedDict(ctx context.Context, st *dictionary.Store) error {
	type item struct {
		category string
		code     string
		label    string
		sortNo   int
	}
	items := []item{
		// 工序（检验工序）：表单的第一列，也是"找到自己工序所在表单"的依据。
		// 工序引用稳定字典 ID，后续可以维护。
		{dictionary.CategoryInspectionOp, "GENERAL", "一般检验", 0},
		{dictionary.CategoryInspectionOp, "一次加硬", "一次加硬", 10},
		{dictionary.CategoryInspectionOp, "二次加硬", "二次加硬", 20},

		// 样品信息：项目（M1/M2）必须可录入（B-7）
		{dictionary.CategorySampleProject, "M1", "M1", 10},
		{dictionary.CategorySampleProject, "M2", "M2", 20},

		// 型号（附加成分的含义未确认，仅作为可选值，冗余处理）
		{dictionary.CategorySampleModel, "VID1", "VID1", 10},
		{dictionary.CategorySampleModel, "VID1 Rx", "VID1 Rx", 11},
		{dictionary.CategorySampleModel, "VID2", "VID2", 20},

		// 规格 / 大小 / 左右
		{dictionary.CategorySampleSpec, "S1", "S1", 10},
		{dictionary.CategorySampleSpec, "S2", "S2", 20},
		{dictionary.CategorySampleSize, "MD", "MD", 10},
		{dictionary.CategorySampleSize, "SM", "SM", 20},
		{dictionary.CategorySampleSize, "LG", "LG", 30},
		{dictionary.CategorySampleSide, "L", "L", 10},
		{dictionary.CategorySampleSide, "R", "R", 20},

		// 结构类别（含义未确认，仅作冗余维度）
		{dictionary.CategorySampleClass, "MONOC", "MONOC", 10},
		{dictionary.CategorySampleClass, "BINOC", "BINOC", 20},
		{dictionary.CategorySampleClass, "MONOC_AND_BINOC", "MONOC & BINOC", 30},
	}
	for _, it := range items {
		if err := st.InitializeItem(ctx, &dictionary.Item{
			Category: it.category, Code: it.code, Label: it.label, SortNo: it.sortNo, Active: true,
		}); err != nil {
			return err
		}
	}
	log.Printf("[info] 字典项已写入: %d 条", len(items))
	return nil
}

// seedDrawings 读取 CSV 并写入参考部件号。
func seedDrawings(ctx context.Context, st *dictionary.Store) error {
	f, err := os.Open(drawingsFile)
	if err != nil {
		log.Printf("[warn] 跳过参考部件号导入（未找到 %s: %v）", drawingsFile, err)
		return nil
	}
	defer func() { _ = f.Close() }()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return err
	}

	list := make([]dictionary.Drawing, 0, len(rows))
	for i, row := range rows {
		if i == 0 || len(row) < 2 {
			continue // 表头或空行
		}
		seq := strings.TrimSpace(row[0])
		drawing := strings.TrimSpace(row[1])
		if drawing == "" {
			continue
		}
		d := dictionary.Drawing{DrawingNo: drawing, Active: true}
		if seq != "" {
			s := seq
			d.SeqNo = &s
		}
		applyDimensions(&d, drawing)
		list = append(list, d)
	}
	inserted, err := st.InitializeDrawings(ctx, list)
	if err != nil {
		return err
	}
	log.Printf("[info] 参考部件号: 新增 %d，更新 %d，合计 %d", inserted, 0, len(list))
	return nil
}

// applyDimensions 按绘图号的书写顺序（规格-大小-左右-型号-结构类别）尽力拆解。
func applyDimensions(d *dictionary.Drawing, drawing string) {
	upper := strings.ToUpper(drawing)
	tokens := strings.FieldsFunc(upper, func(r rune) bool {
		return r == '-' || r == ' ' || r == '_'
	})
	for _, tok := range tokens {
		switch tok {
		case "S1", "S2":
			v := tok
			d.SpecCode = &v
		case "MD", "SM", "LG":
			v := tok
			d.SizeCode = &v
		case "L", "R":
			v := tok
			d.SideCode = &v
		case "VID1", "VID2":
			v := tok
			d.ModelCode = &v
		case "RX":
			v := true
			d.IsRx = &v
		case "MONOC":
			v := "MONOC"
			d.ClassCode = &v
		case "BINOC":
			v := "BINOC"
			d.ClassCode = &v
		}
	}
	if strings.Contains(upper, "MONOC") && strings.Contains(upper, "BINOC") {
		v := "MONOC_AND_BINOC"
		d.ClassCode = &v
	}
}
