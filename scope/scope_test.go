package scope

import (
	"testing"

	"github.com/akamajoris/ngorm/engine"
	"github.com/akamajoris/ngorm/fixture"
	"github.com/akamajoris/ngorm/model"
	"github.com/akamajoris/ql"
)

func TestFieldByName(t *testing.T) {
	e := fixture.TestEngine()
	var field fixture.CalculateField
	_, err := FieldByName(e, &field, "Children")
	if err != nil {
		t.Error(err)
	}
}

func TestQuote(t *testing.T) {
	e := fixture.TestEngine()
	e.Dialect = &ql.QL{}
	sample := []struct {
		src, expetc string
	}{
		{"quote", `quote`},
		{"quote.quote.quote", `quote.quote.quote`},
	}

	for _, v := range sample {
		q := Quote(e, v.src)
		if q != v.expetc {
			t.Errorf("expected %s got %s", v.expetc, q)
		}
	}
}

func TestQuotedTableName(t *testing.T) {
	e := fixture.TestEngine()
	e.Dialect = &ql.QL{}
	tname := "my_table"
	e.Search.TableName = tname
	name := QuotedTableName(e, tname)
	if name != Quote(e, tname) {
		t.Errorf("expected %s got %s", Quote(e, tname), name)
	}
}

func TestPrimaryKey(t *testing.T) {
	e := fixture.TestEngine()
	e.Dialect = &ql.QL{}
	expect := "mapped_id"
	key, err := PrimaryKey(e, &fixture.CustomizeColumn{ID: 10})
	if err != nil {
		t.Fatal(err)
	}
	if key != expect {
		t.Errorf("expected %s got %s", expect, key)
	}
}

type withTabler struct {
	model.Model
}

func (w *withTabler) TableName() string {
	return "with_tabler"
}

type withDBTabler struct {
	model.Model
}

func (w *withDBTabler) TableName(e *engine.Engine) string {
	return "with_tabler"
}

func TestTableName(t *testing.T) {
	e := fixture.TestEngine()
	e.Dialect = &ql.QL{}
	table := "serach_table"
	tabler := "with_tabler"
	e.Search.TableName = table

	// When the serach has table name set
	name := TableName(e, &withTabler{})
	if name != table {
		t.Errorf("expected %s got %s", table, name)
	}
	e.Search = nil
	name = TableName(e, &withTabler{})
	if name != tabler {
		t.Errorf("expected %s got %s", tabler, name)
	}
	name = TableName(e, &withDBTabler{})
	if name != tabler {
		t.Errorf("expected %s got %s", tabler, name)
	}
	name = TableName(e, &model.Model{})
	expect := "models"
	if name != expect {
		t.Errorf("expected %s got %s", expect, name)
	}

}
