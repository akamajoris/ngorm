// Package scope defines functions that operates on  engine.Engine and  enables
// operating on model values easily.
//
// Scope adds a layer of encapsulation on the model on which we are using to
// compose Queries or interact with the database
package scope

import (
	"database/sql"
	"errors"
	"fmt"
	"go/ast"
	"reflect"
	"strings"
	"time"

	"github.com/gernest/ngorm/engine"
	"github.com/gernest/ngorm/model"
	"github.com/gernest/ngorm/util"
	"github.com/jinzhu/inflection"
)

//Quote quotes the str into an SQL string. This makes sure sql strings have ""
//around them.
//
// For the case of a str which has a dot in it example one.two the string is
// quoted and becomes "one"."two" and the quote implementation is called from
// the e.Parent.Dialect.
//
// In case of a string without a dot example one it will be quoted using the
// current dialect e.Dialect
//
//TODO: (gernest) Understand why we use the Parent.Dialect here as it seems
//unlikely the dialect to be different.
func Quote(e *engine.Engine, str string) string {
	if strings.Index(str, ".") != -1 {
		newStrs := []string{}
		for _, s := range strings.Split(str, ".") {
			newStrs = append(newStrs, e.Dialect.Quote(s))
		}
		return strings.Join(newStrs, ".")
	}
	return e.Dialect.Quote(str)
}

//Fields extracts []*model.Fields from value, value is obvously a struct or
//something. This is only done when e.Scope.Fields is nil, for the case of non
//nil value then *e.Scope.Fiedls is returned without computing anything.
func Fields(e *engine.Engine, value interface{}) ([]*model.Field, error) {
	var fields []*model.Field
	i := reflect.ValueOf(value)
	if i.Kind() == reflect.Ptr {
		i = i.Elem()
	}
	isStruct := i.Kind() == reflect.Struct
	m, err := GetModelStruct(e, value)
	if err != nil {
		return nil, err
	}
	for _, structField := range m.StructFields {
		if isStruct {
			fieldValue := i
			for _, name := range structField.Names {
				fieldValue = reflect.Indirect(fieldValue).FieldByName(name)
			}
			fields = append(fields, &model.Field{
				StructField: structField,
				Field:       fieldValue,
				IsBlank:     util.IsBlank(fieldValue)})
		} else {
			fields = append(fields, &model.Field{
				StructField: structField,
				IsBlank:     true})
		}
	}
	return fields, nil
}

//GetModelStruct construct a *model.Struct from value. This does not set
//the e.Scope.Value to value, you must set this value manually if you want to
//set the scope value.
//
// value must be a go struct or a slict of go struct. The computed *model.Struct is cached , so
// multiple calls to this function with the same value won't compute anything
// and return the cached copy. It is less unlikely that the structs will be
// changine at runtime.
//
// The value can implement engine.Tabler interface to help easily identify the
// table name for the model.
func GetModelStruct(e *engine.Engine, value interface{}) (*model.Struct, error) {
	var m model.Struct
	// Scope value can't be nil
	if value == nil {
		return nil, errors.New("nil value")
	}

	refType := reflect.ValueOf(value).Type()
	if refType.Kind() == reflect.Ptr {
		refType = refType.Elem()
	}
	if refType.Kind() == reflect.Slice {
		refType = refType.Elem()
		if refType.Kind() == reflect.Ptr {
			refType = refType.Elem()
		}
	}

	// Scope value need to be a struct
	if refType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%s is not supported, value should be a struct%s ", refType.Kind())
	}

	// Get Cached model struct
	if v := e.StructMap.Get(refType); v != nil {
		return v, nil
	}

	m.ModelType = refType

	// Set default table name
	if tabler, ok := reflect.New(refType).Interface().(engine.Tabler); ok {
		m.DefaultTableName = tabler.TableName()
	} else {
		tableName := util.ToDBName(refType.Name())

		// In case we have set SingulaTable to false, then we pluralize the
		// table name. For example session becomes sessions.
		if !e.SingularTable {
			tableName = inflection.Plural(tableName)
		}
		m.DefaultTableName = tableName
	}

	// Get all fields
	for i := 0; i < refType.NumField(); i++ {
		if fStruct := refType.Field(i); ast.IsExported(fStruct.Name) {
			field := &model.StructField{
				Struct:      fStruct,
				Name:        fStruct.Name,
				Names:       []string{fStruct.Name},
				Tag:         fStruct.Tag,
				TagSettings: model.ParseTagSetting(fStruct.Tag),
			}

			// is ignored field
			if _, ok := field.TagSettings["-"]; ok {
				field.IsIgnored = true
			} else {
				if _, ok := field.TagSettings["PRIMARY_KEY"]; ok {
					field.IsPrimaryKey = true
					m.PrimaryFields = append(m.PrimaryFields, field)
				}

				if _, ok := field.TagSettings["DEFAULT"]; ok {
					field.HasDefaultValue = true
				}

				if _, ok := field.TagSettings["AUTO_INCREMENT"]; ok && !field.IsPrimaryKey {
					field.HasDefaultValue = true
				}

				inType := fStruct.Type
				for inType.Kind() == reflect.Ptr {
					inType = inType.Elem()
				}

				fieldValue := reflect.New(inType).Interface()
				if _, isScanner := fieldValue.(sql.Scanner); isScanner {
					// is scanner
					field.IsScanner, field.IsNormal = true, true
					if inType.Kind() == reflect.Struct {
						for i := 0; i < inType.NumField(); i++ {
							for key, value := range model.ParseTagSetting(inType.Field(i).Tag) {
								field.TagSettings[key] = value
							}
						}
					}
				} else if _, isTime := fieldValue.(*time.Time); isTime {
					// is time
					field.IsNormal = true
				} else if _, ok := field.TagSettings["EMBEDDED"]; ok || fStruct.Anonymous {
					// is embedded struct
					ms, err := GetModelStruct(e, fieldValue)
					if err != nil {
						return nil, err
					}
					for _, subField := range ms.StructFields {
						subField = subField.Clone()
						subField.Names = append([]string{fStruct.Name}, subField.Names...)
						if prefix, ok := field.TagSettings["EMBEDDED_PREFIX"]; ok {
							subField.DBName = prefix + subField.DBName
						}
						if subField.IsPrimaryKey {
							m.PrimaryFields = append(m.PrimaryFields, subField)
						}
						m.StructFields = append(m.StructFields, subField)
					}
					continue
				} else {
					// build relationships
					switch inType.Kind() {
					case reflect.Slice:
						defer buildRelationSlice(e, value, refType, &m, field)

					case reflect.Struct:
						defer buildRelationStruct(e, value, refType, &m, field)
					default:
						field.IsNormal = true
					}
				}
			}

			// Even it is ignored, also possible to decode db value into the field
			if value, ok := field.TagSettings["COLUMN"]; ok {
				field.DBName = value
			} else {
				field.DBName = util.ToDBName(fStruct.Name)
			}

			m.StructFields = append(m.StructFields, field)
		}
	}

	if len(m.PrimaryFields) == 0 {
		if field := GetForeignField("id", m.StructFields); field != nil {
			field.IsPrimaryKey = true
			m.PrimaryFields = append(m.PrimaryFields, field)
		}
	}

	e.StructMap.Set(refType, &m)
	return &m, nil
}

//BuildRelationSlice builds relationship for a field of kind reflect.Slice. This
//updates the ModelStruct m accordingly.
//
//TODO: (gernest) Proper error handling.Make sure we return error, this is a lot
//of loggic and no any error should be absorbed.
func buildRelationSlice(e *engine.Engine, modelValue interface{}, refType reflect.Type, m *model.Struct, field *model.StructField) error {
	var (
		rel                    = &model.Relationship{}
		toScope                = reflect.New(field.Struct.Type).Interface()
		fks                    []string
		associationForeignKeys []string
		elemType               = field.Struct.Type
	)

	if fk := field.TagSettings["FOREIGNKEY"]; fk != "" {
		fks = strings.Split(field.TagSettings["FOREIGNKEY"], ",")
	}

	if fk := field.TagSettings["ASSOCIATIONFOREIGNKEY"]; fk != "" {
		associationForeignKeys = strings.Split(field.TagSettings["ASSOCIATIONFOREIGNKEY"], ",")
	}

	for elemType.Kind() == reflect.Slice || elemType.Kind() == reflect.Ptr {
		elemType = elemType.Elem()
	}

	if elemType.Kind() == reflect.Struct {
		if many2many := field.TagSettings["MANY2MANY"]; many2many != "" {
			rel.Kind = "many_to_many"

			// if no foreign keys defined with tag
			if len(fks) == 0 {
				for _, field := range m.PrimaryFields {
					fks = append(fks, field.DBName)
				}
			}

			for _, fk := range fks {
				if foreignField := GetForeignField(fk, m.StructFields); foreignField != nil {
					// source foreign keys (db names)
					rel.ForeignFieldNames = append(rel.ForeignFieldNames, foreignField.DBName)
					// join table foreign keys for source
					joinTableDBName := util.ToDBName(refType.Name()) + "_" + foreignField.DBName
					rel.ForeignDBNames = append(rel.ForeignDBNames, joinTableDBName)
				}
			}

			// if no association foreign keys defined with tag
			if len(associationForeignKeys) == 0 {
				pf, err := PrimaryFields(e, toScope)
				if err != nil {
					return err
				}
				for _, field := range pf {
					associationForeignKeys = append(associationForeignKeys, field.DBName)
				}
			}

			for _, name := range associationForeignKeys {
				field, err := FieldByName(e, toScope, name)
				if err != nil {
					return err
				}
				// association foreign keys (db names)
				rel.AssociationForeignFieldNames = append(rel.AssociationForeignFieldNames, field.DBName)
				// join table foreign keys for association
				joinTableDBName := util.ToDBName(elemType.Name()) + "_" + field.DBName
				rel.AssociationForeignDBNames = append(rel.AssociationForeignDBNames, joinTableDBName)
			}

			//joinTableHandler := JoinTableHandler{}
			//joinTableHandler.Setup(relationship, many2many, refType, elemType)
			//relationship.JoinTableHandler = &joinTableHandler
			field.Relationship = rel
		} else {
			// User has many comments, associationType is User, comment use UserID as foreign key
			var associationType = refType.Name()
			ms, err := GetModelStruct(e, toScope)
			if err != nil {
				return err
			}
			var toFields = ms.StructFields
			rel.Kind = "has_many"

			if polymorphic := field.TagSettings["POLYMORPHIC"]; polymorphic != "" {
				// Dog has many toys, tag polymorphic is Owner, then associationType is Owner
				// Toy use OwnerID, OwnerType ('dogs') as foreign key
				if polymorphicType := GetForeignField(polymorphic+"Type", toFields); polymorphicType != nil {
					associationType = polymorphic
					rel.PolymorphicType = polymorphicType.Name
					rel.PolymorphicDBName = polymorphicType.DBName
					// if Dog has multiple set of toys set name of the set (instead of default 'dogs')
					if value, ok := field.TagSettings["POLYMORPHIC_VALUE"]; ok {
						rel.PolymorphicValue = value
					} else {
						rel.PolymorphicValue = e.Search.TableName
					}
					polymorphicType.IsForeignKey = true
				}
			}

			// if no foreign keys defined with tag
			if len(fks) == 0 {
				// if no association foreign keys defined with tag
				if len(associationForeignKeys) == 0 {
					for _, field := range m.PrimaryFields {
						fks = append(fks, associationType+field.Name)
						associationForeignKeys = append(associationForeignKeys, field.Name)
					}
				} else {
					// generate foreign keys from defined association foreign keys
					for _, scopeFieldName := range associationForeignKeys {
						if foreignField := GetForeignField(scopeFieldName, m.StructFields); foreignField != nil {
							fks = append(fks, associationType+foreignField.Name)
							associationForeignKeys = append(associationForeignKeys, foreignField.Name)
						}
					}
				}
			} else {
				// generate association foreign keys from foreign keys
				if len(associationForeignKeys) == 0 {
					for _, fk := range fks {
						if strings.HasPrefix(fk, associationType) {
							associationForeignKey := strings.TrimPrefix(fk, associationType)
							if foreignField := GetForeignField(associationForeignKey, m.StructFields); foreignField != nil {
								associationForeignKeys = append(associationForeignKeys, associationForeignKey)
							}
						}
					}
					if len(associationForeignKeys) == 0 && len(fks) == 1 {
						pk, err := PrimaryKey(e, modelValue)
						if err != nil {
							return err
						}
						associationForeignKeys = []string{pk}
					}
				} else if len(fks) != len(associationForeignKeys) {
					return errors.New("invalid foreign keys, should have same length")
				}
			}

			for idx, fk := range fks {
				if foreignField := GetForeignField(fk, toFields); foreignField != nil {
					if associationField := GetForeignField(associationForeignKeys[idx], m.StructFields); associationField != nil {
						// source foreign keys
						foreignField.IsForeignKey = true
						rel.AssociationForeignFieldNames = append(rel.AssociationForeignFieldNames, associationField.Name)
						rel.AssociationForeignDBNames = append(rel.AssociationForeignDBNames, associationField.DBName)

						// association foreign keys
						rel.ForeignFieldNames = append(rel.ForeignFieldNames, foreignField.Name)
						rel.ForeignDBNames = append(rel.ForeignDBNames, foreignField.DBName)
					}
				}
			}

			if len(rel.ForeignFieldNames) != 0 {
				field.Relationship = rel
			}
		}
	} else {
		field.IsNormal = true
	}
	return nil
}

//BuildRelationStruct builds relationship for a field of kind reflect.Struct . This
//updates the ModelStruct m accordingly.
//
//TODO: (gernest) Proper error handling.Make sure we return error, this is a lot
//of loggic and no any error should be absorbed.
func buildRelationStruct(e *engine.Engine, modelValue interface{}, refType reflect.Type, m *model.Struct, field *model.StructField) error {
	var (
		// user has one profile, associationType is User, profile use UserID as foreign key
		// user belongs to profile, associationType is Profile, user use ProfileID as foreign key
		associationType           = refType.Name()
		rel                       = &model.Relationship{}
		toScope                   = reflect.New(field.Struct.Type).Interface()
		tagForeignKeys            []string
		tagAssociationForeignKeys []string
	)
	ms, err := GetModelStruct(e, toScope)
	if err != nil {
		return err
	}
	toFields := ms.StructFields

	if fk := field.TagSettings["FOREIGNKEY"]; fk != "" {
		tagForeignKeys = strings.Split(field.TagSettings["FOREIGNKEY"], ",")
	}

	if fk := field.TagSettings["ASSOCIATIONFOREIGNKEY"]; fk != "" {
		tagAssociationForeignKeys = strings.Split(field.TagSettings["ASSOCIATIONFOREIGNKEY"], ",")
	}

	if polymorphic := field.TagSettings["POLYMORPHIC"]; polymorphic != "" {
		// Cat has one toy, tag polymorphic is Owner, then associationType is Owner
		// Toy use OwnerID, OwnerType ('cats') as foreign key
		if polymorphicType := GetForeignField(polymorphic+"Type", toFields); polymorphicType != nil {
			associationType = polymorphic
			rel.PolymorphicType = polymorphicType.Name
			rel.PolymorphicDBName = polymorphicType.DBName
			// if Cat has several different types of toys set name for each (instead of default 'cats')
			if value, ok := field.TagSettings["POLYMORPHIC_VALUE"]; ok {
				rel.PolymorphicValue = value
			} else {
				rel.PolymorphicValue = TableName(e, modelValue)
			}
			polymorphicType.IsForeignKey = true
		}
	}

	// Has One
	{
		var fks = tagForeignKeys
		var associationForeignKeys = tagAssociationForeignKeys
		// if no foreign keys defined with tag
		if len(fks) == 0 {
			// if no association foreign keys defined with tag
			if len(associationForeignKeys) == 0 {
				for _, primaryField := range m.PrimaryFields {
					fks = append(fks, associationType+primaryField.Name)
					associationForeignKeys = append(associationForeignKeys, primaryField.Name)
				}
			} else {
				// generate foreign keys form association foreign keys
				for _, associationForeignKey := range tagAssociationForeignKeys {
					if foreignField := GetForeignField(associationForeignKey, m.StructFields); foreignField != nil {
						fks = append(fks, associationType+foreignField.Name)
						associationForeignKeys = append(associationForeignKeys, foreignField.Name)
					}
				}
			}
		} else {
			// generate association foreign keys from foreign keys
			if len(associationForeignKeys) == 0 {
				for _, fk := range fks {
					if strings.HasPrefix(fk, associationType) {
						associationForeignKey := strings.TrimPrefix(fk, associationType)
						if foreignField := GetForeignField(associationForeignKey, m.StructFields); foreignField != nil {
							associationForeignKeys = append(associationForeignKeys, associationForeignKey)
						}
					}
				}
				if len(associationForeignKeys) == 0 && len(fks) == 1 {
					pk, err := PrimaryKey(e, modelValue)
					if err != nil {
						return err
					}
					associationForeignKeys = []string{pk}
				}
			} else if len(fks) != len(associationForeignKeys) {
				return errors.New("invalid foreign keys, should have same length")
			}
		}

		for idx, fk := range fks {
			if foreignField := GetForeignField(fk, toFields); foreignField != nil {
				if scopeField := GetForeignField(associationForeignKeys[idx], m.StructFields); scopeField != nil {
					foreignField.IsForeignKey = true
					// source foreign keys
					rel.AssociationForeignFieldNames = append(rel.AssociationForeignFieldNames, scopeField.Name)
					rel.AssociationForeignDBNames = append(rel.AssociationForeignDBNames, scopeField.DBName)

					// association foreign keys
					rel.ForeignFieldNames = append(rel.ForeignFieldNames, foreignField.Name)
					rel.ForeignDBNames = append(rel.ForeignDBNames, foreignField.DBName)
				}
			}
		}
	}

	if len(rel.ForeignFieldNames) != 0 {
		rel.Kind = "has_one"
		field.Relationship = rel
	} else {
		var fks = tagForeignKeys
		var associationForeignKeys = tagAssociationForeignKeys

		if len(fks) == 0 {
			// generate foreign keys & association foreign keys
			if len(associationForeignKeys) == 0 {
				pf, err := PrimaryFields(e, toScope)
				if err != nil {
					return err
				}
				for _, primaryField := range pf {
					fks = append(fks, field.Name+primaryField.Name)
					associationForeignKeys = append(associationForeignKeys, primaryField.Name)
				}
			} else {
				// generate foreign keys with association foreign keys
				for _, associationForeignKey := range associationForeignKeys {
					if foreignField := GetForeignField(associationForeignKey, toFields); foreignField != nil {
						fks = append(fks, field.Name+foreignField.Name)
						associationForeignKeys = append(associationForeignKeys, foreignField.Name)
					}
				}
			}
		} else {
			// generate foreign keys & association foreign keys
			if len(associationForeignKeys) == 0 {
				for _, fk := range fks {
					if strings.HasPrefix(fk, field.Name) {
						associationForeignKey := strings.TrimPrefix(fk, field.Name)
						if foreignField := GetForeignField(associationForeignKey, toFields); foreignField != nil {
							associationForeignKeys = append(associationForeignKeys, associationForeignKey)
						}
					}
				}
				if len(associationForeignKeys) == 0 && len(fks) == 1 {
					pk, err := PrimaryKey(e, toScope)
					if err != nil {
						return err
					}
					associationForeignKeys = []string{pk}
				}
			} else if len(fks) != len(associationForeignKeys) {
				return errors.New("invalid foreign keys, should have same length")
			}
		}

		for idx, fk := range fks {
			if foreignField := GetForeignField(fk, m.StructFields); foreignField != nil {
				if associationField := GetForeignField(associationForeignKeys[idx], toFields); associationField != nil {
					foreignField.IsForeignKey = true

					// association foreign keys
					rel.AssociationForeignFieldNames = append(rel.AssociationForeignFieldNames, associationField.Name)
					rel.AssociationForeignDBNames = append(rel.AssociationForeignDBNames, associationField.DBName)

					// source foreign keys
					rel.ForeignFieldNames = append(rel.ForeignFieldNames, foreignField.Name)
					rel.ForeignDBNames = append(rel.ForeignDBNames, foreignField.DBName)
				}
			}
		}

		if len(rel.ForeignFieldNames) != 0 {
			rel.Kind = "belongs_to"
			field.Relationship = rel
		}
	}
	return nil
}

//FieldByName returns the field in the model struct value with name name.
//
//TODO:(gernest) return an error when the field is not found.
func FieldByName(e *engine.Engine, value interface{}, name string) (*model.Field, error) {
	dbName := util.ToDBName(name)
	fds, err := Fields(e, value)
	if err != nil {
		return nil, err
	}
	for _, field := range fds {
		if field.Name == name || field.DBName == name {
			return field, nil
		} else {
			if field.DBName == dbName {
				return field, nil
			}
		}
	}
	return nil, errors.New("field not found")
}

//PrimaryFields returns fields that have PRIMARY_KEY Tab from the struct value.
func PrimaryFields(e *engine.Engine, value interface{}) ([]*model.Field, error) {
	var fields []*model.Field
	fds, err := Fields(e, value)
	if err != nil {
		return nil, err
	}
	for _, field := range fds {
		if field.IsPrimaryKey {
			fields = append(fields, field)
		}
	}
	return fields, nil
}

//PrimaryField returns the field with name id, or any primary field that happens
//to be the one defined by the model value.
func PrimaryField(e *engine.Engine, value interface{}) (*model.Field, error) {
	m, err := GetModelStruct(e, value)
	if err != nil {
		return nil, err
	}
	if primaryFields := m.PrimaryFields; len(primaryFields) > 0 {
		if len(primaryFields) > 1 {
			field, err := FieldByName(e, value, "id")
			if err != nil {
				return nil, err
			}
			return field, nil
		}
		pf, err := PrimaryFields(e, value)
		if err != nil {
			return nil, err
		}
		return pf[0], nil
	}
	return nil, errors.New("no field found")
}

// TableName returns a string representation of the possible name of the table
// that is mapped to the model value.
//
// If it happens that the model value implements engine.Tabler interface then we
// go with it.
//
// In case we are in search mode, the Tablename inside the e.Search.TableName is
// what we use.
func TableName(e *engine.Engine, value interface{}) string {
	if e.Search != nil && len(e.Search.TableName) > 0 {
		return e.Search.TableName
	}

	if tabler, ok := value.(engine.Tabler); ok {
		return tabler.TableName()
	}

	if tabler, ok := value.(engine.DBTabler); ok {
		return tabler.TableName(e)
	}
	ms, err := GetModelStruct(e, value)
	if err != nil {
		//TODO log this?
		return ""
	}
	return ms.DefaultTableName
}

//PrimaryKey returns the name of the primary key for the model value
func PrimaryKey(e *engine.Engine, value interface{}) (string, error) {
	pf, err := PrimaryField(e, value)
	if err != nil {
		return "", err
	}
	return pf.DBName, nil
}

//QuotedTableName  returns a quoted table name.
func QuotedTableName(e *engine.Engine, value interface{}) string {
	if e.Search != nil && len(e.Search.TableName) > 0 {
		if strings.Index(e.Search.TableName, " ") != -1 {
			return e.Search.TableName
		}
		return Quote(e, e.Search.TableName)
	}

	return Quote(e, TableName(e, value))
}

//AddToVars add value to e.Scope.SQLVars it returns  the positional binding of
//the values.
//
// The way positional arguments are handled inthe database/sql package relies on
// database specific setting.
//
// For instance in ql
//    $1 will bind the value of the first argument.
//
// The returned string depends on implementation provided by the
// Dialect.BindVar, the number that is passed to BindVar is based on the number
// of items stored in e.Scope.SQLVars. So if the length is 4 it might be $4 for
// the ql dialect.
//
// It is possible to supply *model.Expr as value. The expression will be
// evaluated accordingly by replacing each occurrence of ? in *model.Expr.Q with
// the positional binding of the *model.Expr.Arg item.
func AddToVars(e *engine.Engine, value interface{}) string {
	if expr, ok := value.(*model.Expr); ok {
		exp := expr.Q
		for _, arg := range expr.Args {
			exp = strings.Replace(exp, "?", AddToVars(e, arg), 1)
		}
		return exp
	}

	e.Scope.SQLVars = append(e.Scope.SQLVars, value)
	return e.Dialect.BindVar(len(e.Scope.SQLVars))
}

//HasColumn returns true if the modelValue has column of name column.
func HasColumn(e *engine.Engine, modelValue interface{}, column string) bool {
	ms, err := GetModelStruct(e, modelValue)
	if err != nil {
		//TODO log this?
		return false
	}
	for _, field := range ms.StructFields {
		if field.IsNormal && (field.Name == column || field.DBName == column) {
			return true
		}
	}
	return false
}

//GetForeignField return the foreign field among the supplied fields.
func GetForeignField(column string, fields []*model.StructField) *model.StructField {
	for _, field := range fields {
		if field.Name == column || field.DBName == column || field.DBName == util.ToDBName(column) {
			return field
		}
	}
	return nil
}

func Scan(rows *sql.Rows, columns []string, fields []*model.Field) {
}
