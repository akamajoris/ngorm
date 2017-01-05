package builder

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gernest/ngorm/dialects/ql"
	"github.com/gernest/ngorm/fixture"
	"github.com/gernest/ngorm/search"
)

func TestGroup(t *testing.T) {
	e := fixture.TestEngine()
	s := GroupSQL(e)
	if s != "" {
		t.Errorf("expected an empty string got %s", s)
	}
	by := "location"
	search.Group(e, by)
	s = GroupSQL(e)
	expect := " GROUP BY " + by
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

}

func TestLimitAndOffsetSQL(t *testing.T) {
	e := fixture.TestEngine()
	e.Dialect = ql.Memory()
	limit := 2
	offset := 4
	search.Limit(e, limit)
	search.Offset(e, offset)
	expect := fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	s := LimitAndOffsetSQL(e)
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

}

func TestPrepareQuerySQL(t *testing.T) {
	e := fixture.TestEngine()
	e.Dialect = ql.Memory()
	search.Limit(e, 1)
	search.Where(e, "name=?", "gernest")
	var user fixture.User
	s, err := PrepareQuerySQL(e, &user)
	if err != nil {
		//t.Error(err)
	}
	fmt.Println(s)
}

func TestWhere(t *testing.T) {
	e := fixture.TestEngine()
	e.Dialect = ql.Memory()

	// Where using Plain SQL
	search.Where(e, "name=?", "gernest")
	var user fixture.User
	s, err := Where(e, &user, e.Search.WhereConditions[0])
	if err != nil {
		t.Fatal(err)
	}
	expect := "(name=$1)"
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

	// IN
	search.Where(e, "name in (?)", []string{"jinzhu", "jinzhu 2"})
	s, err = Where(e, &user, e.Search.WhereConditions[1])
	if err != nil {
		t.Fatal(err)
	}
	expect = "(name in ($2,$3))"
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

	// LIKE
	search.Where(e, "name LIKE ?", "%jin%")
	s, err = Where(e, &user, e.Search.WhereConditions[2])
	if err != nil {
		t.Fatal(err)
	}
	expect = "(name LIKE $4)"
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

	// AND
	search.Where(e, "name = ? AND age >= ?", "jinzhu", "22")
	s, err = Where(e, &user, e.Search.WhereConditions[3])
	if err != nil {
		t.Fatal(err)
	}
	expect = "(name = $5 AND age >= $6)"
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

	// Where with  Map
	e.Search.WhereConditions = nil
	e.Scope.SQLVars = nil
	search.Where(e, map[string]interface{}{"name": "jinzhu", "age": 20})
	s, err = Where(e, &user, e.Search.WhereConditions[0])
	if err != nil {
		t.Fatal(err)
	}
	expect = `("users"."name"`
	if !strings.Contains(s, expect) {
		t.Errorf("expected %s to containe %s", s, expect)
	}

	// Map when value is nil
	e.Search.WhereConditions = nil
	e.Scope.SQLVars = nil
	search.Where(e, map[string]interface{}{"name": "jinzhu", "age": nil})
	s, err = Where(e, &user, e.Search.WhereConditions[0])
	if err != nil {
		t.Fatal(err)
	}
	expected := `("users"."age" IS NULL)`
	if !strings.Contains(s, expected) {
		t.Errorf("expected %s to contain %s", s, expected)
	}

	// Primary Key
	e.Search.WhereConditions = nil
	e.Scope.SQLVars = nil
	search.Where(e, 10)
	s, err = Where(e, &user, e.Search.WhereConditions[0])
	if err != nil {
		t.Fatal(err)
	}
	expect = `("users"."id" = $1)`
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

	/// Slice of primary Keys
	e.Search.WhereConditions = nil
	e.Scope.SQLVars = nil
	search.Where(e, []int64{20, 21, 22})
	s, err = Where(e, &user, e.Search.WhereConditions[0])
	if err != nil {
		t.Fatal(err)
	}
	expect = `("users"."id" IN ($1,$2,$3))`
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

	// Struct
	e.Search.WhereConditions = nil
	e.Scope.SQLVars = nil
	e.Scope.Fields = nil
	search.Where(e, &fixture.User{Name: "jinzhu", Age: 20})
	s, err = Where(e, &user, e.Search.WhereConditions[0])
	if err != nil {
		t.Fatal(err)
	}
	expect = `("users"."age" = $1) AND ("users"."name" = $2)`
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}
}

func TestNot(t *testing.T) {
	e := fixture.TestEngine()
	e.Dialect = ql.Memory()

	search.Not(e, "name", "gernest")
	var user fixture.User
	s, err := Not(e, &user, e.Search.NotConditions[0])
	if err != nil {
		t.Fatal(err)
	}
	expect := `("users"."name" <> $1)`
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

	// Not in
	e.Search.NotConditions = nil
	e.Scope.SQLVars = nil
	e.Scope.Fields = nil
	search.Not(e, "name", []string{"jinzhu", "jinzhu 2"})
	s, err = Not(e, &user, e.Search.NotConditions[0])
	if err != nil {
		t.Fatal(err)
	}
	expect = `("users"."name" NOT IN ($1,$2))`
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

	// Not in slice of primary keys
	e.Search.NotConditions = nil
	e.Scope.SQLVars = nil
	search.Not(e, []int64{1, 2, 3})
	s, err = Not(e, &user, e.Search.NotConditions[0])
	if err != nil {
		t.Fatal(err)
	}

	expect = `("users"."id" NOT IN ($1,$2,$3))`
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

	// Not in with empty slice
	e.Search.NotConditions = nil
	e.Scope.SQLVars = nil
	search.Not(e, []int64{})
	s, err = Not(e, &user, e.Search.NotConditions[0])
	if err != nil {
		t.Fatal(err)
	}

	expect = ``
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

	// Struct
	e.Search.NotConditions = nil
	e.Scope.SQLVars = nil
	e.Scope.Fields = nil
	search.Not(e, &fixture.Email{Email: "jinzhu"})
	s, err = Not(e, &user, e.Search.NotConditions[0])
	if err != nil {
		t.Fatal(err)
	}

	expect = ``
	if s != expect {
		t.Errorf("expected %s got %s", expect, s)
	}

}
