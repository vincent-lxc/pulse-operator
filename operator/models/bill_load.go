package models

import (
	"database/sql"
	"strconv"
	"strings"
	"sync"

	"github.com/digitalwayhk/core/pkg/persistence/database/oltp"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// loadBills 只读取 GORM 自己的列名。
// 旧库里可能还有 merchantAttempts、latencyMS、x402Required 这种后加的空列。
// SELECT * 会把它们和 merchant_attempts、latency_ms、x402_required 扫进同一个字段，后读到的空值把原来的值盖掉。
func loadBills(where string, arg interface{}, limit int) ([]*Bill, error) {
	if err := ensureModel(NewBill()); err != nil {
		return nil, err
	}
	db, err := billConn()
	if err != nil {
		return nil, err
	}
	cols, err := billSelectColumns(db)
	if err != nil {
		return nil, err
	}
	// 这条连接是进程里共用的。查询单独开 session，避免列清单留在后面的写入上。
	db = db.Session(&gorm.Session{NewDB: true})
	var rows []*Bill
	q := db.Model(NewBill()).Select(cols).Order("id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if where != "" {
		q = q.Where(where, arg)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	if err := fillLegacyBillColumns(db, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func billConn() (*gorm.DB, error) {
	sqlite, ok := getDataAction().(*oltp.Sqlite)
	if !ok {
		return nil, NewBusinessError("账单库不是 SQLite")
	}
	raw, err := sqlite.GetModelDB(NewBill())
	if err != nil {
		return nil, err
	}
	db, ok := raw.(*gorm.DB)
	if !ok || db == nil {
		return nil, NewBusinessError("账单库不可用")
	}
	return db, nil
}

func billSelectColumns(db *gorm.DB) ([]string, error) {
	namer := db.NamingStrategy
	if namer == nil {
		namer = schema.NamingStrategy{SingularTable: true}
	}
	var cache sync.Map
	parsed, err := schema.Parse(NewBill(), &cache, namer)
	if err != nil {
		return nil, err
	}
	cols := make([]string, 0, len(parsed.Fields))
	seen := map[string]bool{}
	for _, field := range parsed.Fields {
		name := strings.TrimSpace(field.DBName)
		if name == "" || !field.Readable || seen[name] {
			continue
		}
		seen[name] = true
		cols = append(cols, name)
	}
	return cols, nil
}

type legacyBillCol struct {
	names []string
	blank func(*Bill) bool
	apply func(*Bill, string)
}

func legacyBillCols() []legacyBillCol {
	return []legacyBillCol{
		{names: []string{"merchantAttempts"}, blank: func(b *Bill) bool { return strings.TrimSpace(b.MerchantAttempts) == "" }, apply: func(b *Bill, v string) { b.MerchantAttempts = v }},
		{names: []string{"x402Required"}, blank: func(b *Bill) bool { return strings.TrimSpace(b.X402Required) == "" }, apply: func(b *Bill, v string) { b.X402Required = v }},
		{names: []string{"riskNotes"}, blank: func(b *Bill) bool { return strings.TrimSpace(b.RiskNotes) == "" }, apply: func(b *Bill, v string) { b.RiskNotes = v }},
		{names: []string{"cctp_message", "cctpMessage"}, blank: func(b *Bill) bool { return strings.TrimSpace(b.CCTPMessageHash) == "" }, apply: func(b *Bill, v string) { b.CCTPMessageHash = v }},
		{names: []string{"forward_fee", "forwardFee"}, blank: func(b *Bill) bool { return strings.TrimSpace(b.ForwardFeeUnits) == "" }, apply: func(b *Bill, v string) { b.ForwardFeeUnits = v }},
		{names: []string{"amountUnits"}, blank: func(b *Bill) bool { return strings.TrimSpace(b.AmountUnits) == "" }, apply: func(b *Bill, v string) { b.AmountUnits = v }},
		{names: []string{"feeAllowanceUnits"}, blank: func(b *Bill) bool { return strings.TrimSpace(b.FeeAllowanceUnits) == "" }, apply: func(b *Bill, v string) { b.FeeAllowanceUnits = v }},
		{names: []string{"categoryCode"}, blank: func(b *Bill) bool { return strings.TrimSpace(b.CategoryCode) == "" }, apply: func(b *Bill, v string) { b.CategoryCode = v }},
		{names: []string{"reasonCode"}, blank: func(b *Bill) bool { return strings.TrimSpace(b.ReasonCode) == "" }, apply: func(b *Bill, v string) { b.ReasonCode = v }},
		{names: []string{"paidAt"}, blank: func(b *Bill) bool { return strings.TrimSpace(b.PaidAt) == "" }, apply: func(b *Bill, v string) { b.PaidAt = v }},
		{names: []string{"latencyMS", "latencyMs", "latency_m_s"}, blank: func(b *Bill) bool { return b.LatencyMS == 0 }, apply: func(b *Bill, v string) {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err == nil && n != 0 {
				b.LatencyMS = n
			}
		}},
	}
}

func fillLegacyBillColumns(db *gorm.DB, rows []*Bill) error {
	if len(rows) == 0 {
		return nil
	}
	present, err := billColumnNames(db)
	if err != nil {
		return err
	}
	type chosen struct {
		legacyBillCol
		actual []string
	}
	var specs []chosen
	var selected []string
	seen := map[string]bool{}
	for _, spec := range legacyBillCols() {
		var actual []string
		for _, name := range spec.names {
			stored, ok := present[strings.ToLower(name)]
			if !ok || seen[strings.ToLower(stored)] {
				continue
			}
			seen[strings.ToLower(stored)] = true
			actual = append(actual, stored)
			selected = append(selected, stored)
		}
		if len(actual) > 0 {
			specs = append(specs, chosen{legacyBillCol: spec, actual: actual})
		}
	}
	if len(selected) == 0 {
		return nil
	}
	byID := map[int64]*Bill{}
	ids := make([]any, 0, len(rows))
	for _, row := range rows {
		if row == nil || row.GetID() == 0 {
			continue
		}
		id := int64(row.GetID())
		byID[id] = row
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	query := "SELECT id, " + quoteCols(selected) + " FROM bill WHERE id IN (" + placeholders + ")"
	sqlRows, err := db.Raw(query, ids...).Rows()
	if err != nil {
		return err
	}
	defer sqlRows.Close()
	for sqlRows.Next() {
		vals := make([]sql.NullString, len(selected))
		dest := make([]any, 0, len(selected)+1)
		var id int64
		dest = append(dest, &id)
		for i := range vals {
			dest = append(dest, &vals[i])
		}
		if err := sqlRows.Scan(dest...); err != nil {
			return err
		}
		row := byID[id]
		if row == nil {
			continue
		}
		offset := 0
		for _, spec := range specs {
			value := ""
			for range spec.actual {
				if vals[offset].Valid && strings.TrimSpace(vals[offset].String) != "" && value == "" {
					value = vals[offset].String
				}
				offset++
			}
			if value != "" && spec.blank(row) {
				spec.apply(row, value)
			}
		}
	}
	return sqlRows.Err()
}

func billColumnNames(db *gorm.DB) (map[string]string, error) {
	type col struct {
		Name string `gorm:"column:name"`
	}
	var cols []col
	if err := db.Raw("PRAGMA table_info(bill)").Scan(&cols).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(cols))
	for _, col := range cols {
		out[strings.ToLower(col.Name)] = col.Name
	}
	return out, nil
}

func quoteCols(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = `CAST("` + strings.ReplaceAll(name, `"`, `""`) + `" AS TEXT)`
	}
	return strings.Join(quoted, ", ")
}
