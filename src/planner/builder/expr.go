/*
 * Radon
 *
 * Copyright 2018 The Radon Authors.
 * Code is licensed under the GPLv3.
 *
 */

package builder

import (
	"fmt"

	"router"

	"github.com/pkg/errors"
	"github.com/xelabs/go-mysqlstack/sqlparser"
)

type exprInfo struct {
	// filter expr.
	expr sqlparser.Expr
	// referred tables.
	referTables []string
	// colname in the filter expr.
	cols []*sqlparser.ColName
	// val in the filter expr.
	vals []*sqlparser.SQLVal
}

// parseWhereOrJoinExprs parse exprs in where or join on conditions.
// eg: 't1.a=t2.a and t1.b=2'.
// t1.a=t2.a paser in joins.
// t1.b=2 paser in wheres, t1.b col, 2 val.
func parseWhereOrJoinExprs(exprs sqlparser.Expr, tbInfos map[string]*tableInfo) ([]exprInfo, []exprInfo, error) {
	filters := splitAndExpression(nil, exprs)
	filters = transformORs(filters)
	var joins, wheres []exprInfo
	for _, filter := range filters {
		var cols []*sqlparser.ColName
		var vals []*sqlparser.SQLVal
		referTables := make([]string, 0, 4)
		err := sqlparser.Walk(func(node sqlparser.SQLNode) (kontinue bool, err error) {
			switch node := node.(type) {
			case *sqlparser.ColName:
				cols = append(cols, node)
				tableName := node.Qualifier.Name.String()
				if tableName == "" {
					if len(tbInfos) == 1 {
						tableName, _ = getOneTableInfo(tbInfos)
					} else {
						return false, errors.Errorf("unsupported: unknown.column.'%s'.in.clause", node.Name.String())
					}
				} else {
					if _, ok := tbInfos[tableName]; !ok {
						return false, errors.Errorf("unsupported: unknown.column.'%s.%s'.in.clause", tableName, node.Name.String())
					}
				}

				if isContainKey(referTables, tableName) {
					return true, nil
				}
				referTables = append(referTables, tableName)
			case *sqlparser.Subquery:
				return false, errors.New("unsupported: subqueries.in.select")
			}
			return true, nil
		}, filter)
		if err != nil {
			return nil, nil, err
		}

		condition, ok := filter.(*sqlparser.ComparisonExpr)
		if ok {
			lc, lok := condition.Left.(*sqlparser.ColName)
			rc, rok := condition.Right.(*sqlparser.ColName)
			switch condition.Operator {
			case sqlparser.EqualStr:
				if lok && rok && lc.Qualifier != rc.Qualifier {
					tuple := exprInfo{condition, referTables, []*sqlparser.ColName{lc, rc}, nil}
					joins = append(joins, tuple)
					continue
				}

				if lok {
					if sqlVal, ok := condition.Right.(*sqlparser.SQLVal); ok {
						vals = append(vals, sqlVal)
					}
				}
				if rok {
					if sqlVal, ok := condition.Left.(*sqlparser.SQLVal); ok {
						vals = append(vals, sqlVal)
						condition.Left, condition.Right = condition.Right, condition.Left
					}
				}
			case sqlparser.InStr:
				if lok {
					if valTuple, ok := condition.Right.(sqlparser.ValTuple); ok {
						var sqlVals []*sqlparser.SQLVal
						isVal := true
						for _, val := range valTuple {
							if sqlVal, ok := val.(*sqlparser.SQLVal); ok {
								sqlVals = append(sqlVals, sqlVal)
							} else {
								isVal = false
								break
							}
						}
						if isVal {
							vals = sqlVals
						}
					}
				}
			}
		}
		tuple := exprInfo{filter, referTables, cols, vals}
		wheres = append(wheres, tuple)
	}
	return joins, wheres, nil
}

// GetDMLRouting used to get the routing from the where clause.
func GetDMLRouting(database, table, shardkey string, where *sqlparser.Where, router *router.Router) ([]router.Segment, error) {
	if shardkey != "" && where != nil {
		filters := splitAndExpression(nil, where.Expr)
		filters = transformORs(filters)
		for _, filter := range filters {
			comparison, ok := filter.(*sqlparser.ComparisonExpr)
			if !ok {
				continue
			}

			// Only deal with Equal statement.
			switch comparison.Operator {
			case sqlparser.EqualStr:
				if nameMatch(comparison.Left, table, shardkey) {
					sqlval, ok := comparison.Right.(*sqlparser.SQLVal)
					if ok {
						return router.Lookup(database, table, sqlval, sqlval)
					}
				}
			case sqlparser.InStr:
				if nameMatch(comparison.Left, table, shardkey) {
					if valTuple, ok := comparison.Right.(sqlparser.ValTuple); ok {
						var idxs []int
						for _, val := range valTuple {
							if sqlVal, ok := val.(*sqlparser.SQLVal); ok {
								idx, err := router.GetIndex(database, table, sqlVal)
								if err != nil {
									return nil, err
								}
								idxs = append(idxs, idx)
							} else {
								return router.Lookup(database, table, nil, nil)
							}
						}
						return router.GetSegments(database, table, idxs)
					}
				}
			}
		}
	}
	return router.Lookup(database, table, nil, nil)
}

func nameMatch(node sqlparser.Expr, table, shardkey string) bool {
	colname, ok := node.(*sqlparser.ColName)
	return ok && (colname.Qualifier.Name.String() == "" || colname.Qualifier.Name.String() == table) && (colname.Name.String() == shardkey)
}

// splitAndExpression breaks up the Expr into AND-separated conditions
// and appends them to filters, which can be shuffled and recombined
// as needed.
func splitAndExpression(filters []sqlparser.Expr, node sqlparser.Expr) []sqlparser.Expr {
	if node == nil {
		return filters
	}
	switch node := node.(type) {
	case *sqlparser.AndExpr:
		filters = splitAndExpression(filters, node.Left)
		return splitAndExpression(filters, node.Right)
	case *sqlparser.ParenExpr:
		return splitAndExpression(filters, node.Expr)
	}
	return append(filters, node)
}

// splitOrExpression breaks up the Expr into OR-separated conditions
// and appends them to filters.
func splitOrExpression(filters []sqlparser.Expr, node sqlparser.Expr) []sqlparser.Expr {
	if node == nil {
		return filters
	}
	switch node := node.(type) {
	case *sqlparser.OrExpr:
		filters = splitOrExpression(filters, node.Left)
		return splitOrExpression(filters, node.Right)
	case *sqlparser.ParenExpr:
		return splitOrExpression(filters, node.Expr)
	}
	return append(filters, node)
}

// transformORs transforms the OR expressions. If the cond is OR, try to extract
// the same condition from it, then convert the OR expression to IN expression.
func transformORs(exprs []sqlparser.Expr) []sqlparser.Expr {
	var newExprs []sqlparser.Expr
	for _, expr := range exprs {
		or, ok := expr.(*sqlparser.OrExpr)
		if !ok {
			newExprs = append(newExprs, expr)
			continue
		}
		subExprs, splited := extractExprsFromOr(or)
		newExprs = append(newExprs, splited...)
		if len(subExprs) > 0 {
			newExprs = append(newExprs, convertOrToIn(subExprs))
		}
	}
	return newExprs
}

// extractExprsFromOr extracts the same condition that occurs in every OR args and remove them from OR.
func extractExprsFromOr(expr *sqlparser.OrExpr) ([][]sqlparser.Expr, []sqlparser.Expr) {
	var exprs []sqlparser.Expr
	exprs = splitOrExpression(exprs, expr)
	subExprs := make([][]sqlparser.Expr, len(exprs))
	strIdxsMap := make(map[string][]int)
	for i, expr := range exprs {
		innerMap := make(map[string]struct{})
		args := splitAndExpression(nil, expr)
		idx := 0
		for idx < len(args) {
			buf := sqlparser.NewTrackedBuffer(nil)
			args[idx].Format(buf)
			str := buf.String()
			if _, ok := innerMap[str]; ok {
				// Remove the duplicate conditions.
				// eg: `(t1.a=1 and t1.a=1) or ...` is equivalent to the statement `t1.a=1 or ...`.
				args = append(args[:i], args[i+1:]...)
				continue
			}
			innerMap[str] = struct{}{}
			if i == 0 {
				strIdxsMap[str] = []int{idx}
			} else if _, ok := strIdxsMap[str]; ok {
				strIdxsMap[str] = append(strIdxsMap[str], idx)
			}
			idx++
		}
		subExprs[i] = args
	}

	// The expr need exists in every subExprs.
	for str, idxs := range strIdxsMap {
		if len(idxs) < len(subExprs) {
			delete(strIdxsMap, str)
		}
	}
	if len(strIdxsMap) == 0 {
		return subExprs, nil
	}

	var splited []sqlparser.Expr
	for _, idxs := range strIdxsMap {
		splited = append(splited, subExprs[0][idxs[0]])
	}

	for _, idxs := range strIdxsMap {
		for i, idx := range idxs {
			subExprs[i] = append(subExprs[i][:idx], subExprs[i][idx+1:]...)
			// eg: `(t1.a=1) or (t1.a=1 and t2.a>1)` is equivalent to the statement `t1.a=1`.
			if len(subExprs[i]) == 0 {
				return nil, splited
			}
		}
	}
	return subExprs, splited
}

// convertOrToIn converts the Expr type from `or` to `in`.
func convertOrToIn(subExprs [][]sqlparser.Expr) sqlparser.Expr {
	var i int
	inMap := make(map[*sqlparser.ColName]sqlparser.ValTuple)
	for i < len(subExprs) {
		if len(subExprs[i]) == 1 {
			col, vals := fetchColVals(subExprs[i][0])
			if col != nil {
				col = checkColInMap(inMap, col)
				inMap[col] = append(inMap[col], vals...)
				subExprs = append(subExprs[:i], subExprs[i+1:]...)
				continue
			}
		}
		i++
	}

	var newExpr sqlparser.Expr
	for _, exprs := range subExprs {
		var sub sqlparser.Expr
		for _, expr := range exprs {
			sub = rebuildAnd(sub, expr)
		}
		newExpr = rebuildOr(newExpr, sub)
	}
	for k, v := range inMap {
		sub := &sqlparser.ComparisonExpr{
			Operator: sqlparser.InStr,
			Left:     k,
			Right:    v,
		}
		newExpr = rebuildOr(newExpr, sub)
	}
	return newExpr
}

// checkColInMap used to check if the colname is in the map.
func checkColInMap(inMap map[*sqlparser.ColName]sqlparser.ValTuple, col *sqlparser.ColName) *sqlparser.ColName {
	for k := range inMap {
		if col.Equal(k) {
			return k
		}
	}
	return col
}

// rebuildOr used to rebuild the OrExpr.
func rebuildOr(node, expr sqlparser.Expr) sqlparser.Expr {
	if node == nil {
		return expr
	}
	if _, ok := node.(*sqlparser.OrExpr); ok {
		node = &sqlparser.ParenExpr{Expr: node}
	}
	return &sqlparser.OrExpr{
		Left:  node,
		Right: expr,
	}
}

// rebuildAnd used to rebuild the AndExpr.
func rebuildAnd(node, expr sqlparser.Expr) sqlparser.Expr {
	if node == nil {
		return expr
	}
	return &sqlparser.AndExpr{
		Left:  node,
		Right: expr,
	}
}

// fetchColVals fetch ColName and ValTuple from Expr which type is `in` or `=`.
func fetchColVals(node sqlparser.Expr) (*sqlparser.ColName, sqlparser.ValTuple) {
	var col *sqlparser.ColName
	var vals sqlparser.ValTuple
	if expr, ok := node.(*sqlparser.ComparisonExpr); ok {
		switch expr.Operator {
		case sqlparser.EqualStr:
			if _, ok := expr.Left.(*sqlparser.SQLVal); ok {
				expr.Left, expr.Right = expr.Right, expr.Left
			}
			if lc, ok := expr.Left.(*sqlparser.ColName); ok {
				if val, ok := expr.Right.(*sqlparser.SQLVal); ok {
					col = lc
					vals = append(vals, val)
				}
			}
		case sqlparser.InStr:
			if lc, ok := expr.Left.(*sqlparser.ColName); ok {
				if valTuple, ok := expr.Right.(sqlparser.ValTuple); ok {
					col = lc
					vals = valTuple
				}
			}
		}
	}
	return col, vals
}

// convertToLeftJoin converts a right join into a left join.
func convertToLeftJoin(joinExpr *sqlparser.JoinTableExpr) {
	newExpr := joinExpr.LeftExpr
	// If LeftExpr is a join, we have to parenthesize it.
	if _, ok := newExpr.(*sqlparser.JoinTableExpr); ok {
		newExpr = &sqlparser.ParenTableExpr{
			Exprs: sqlparser.TableExprs{newExpr},
		}
	}
	joinExpr.LeftExpr, joinExpr.RightExpr = joinExpr.RightExpr, newExpr
	joinExpr.Join = sqlparser.LeftJoinStr
}

// checkJoinOn use to check the join on conditions, according to lpn|rpn to  determine join.cols[0]|cols[1].
// eg: select * from t1 join t2 on t1.a=t2.a join t3 on t2.b=t1.b. 't2.b=t1.b' is forbidden.
func checkJoinOn(lpn, rpn PlanNode, join exprInfo) (exprInfo, error) {
	lt := join.cols[0].Qualifier.Name.String()
	rt := join.cols[1].Qualifier.Name.String()
	if _, ok := lpn.getReferTables()[lt]; ok {
		if _, ok := rpn.getReferTables()[rt]; !ok {
			return join, errors.New("unsupported: join.on.condition.should.cross.left-right.tables")
		}
	} else {
		if _, ok := lpn.getReferTables()[rt]; !ok {
			return join, errors.New("unsupported: join.on.condition.should.cross.left-right.tables")
		}
		join.cols[0], join.cols[1] = join.cols[1], join.cols[0]
	}
	return join, nil
}

// parseHaving used to check the having exprs and parse into tuples.
// unsupport: `select sum(t2.id) as tmp, t1.id from t2,t1 having tmp=1`.
func parseHaving(exprs sqlparser.Expr, fields []selectTuple) ([]exprInfo, error) {
	var tuples []exprInfo
	filters := splitAndExpression(nil, exprs)
	for _, filter := range filters {
		tuple := exprInfo{filter, nil, nil, nil}
		err := sqlparser.Walk(func(node sqlparser.SQLNode) (kontinue bool, err error) {
			switch node := node.(type) {
			case *sqlparser.ColName:
				tuple.cols = append(tuple.cols, node)
				tableName := node.Qualifier.Name.String()
				colName := node.Name.String()
				inField, field := checkInTuple(colName, tableName, fields)
				if !inField {
					col := colName
					if tableName != "" {
						col = fmt.Sprintf("%s.%s", tableName, colName)
					}
					return false, errors.Errorf("unsupported: unknown.column.'%s'.in.having.clause", col)
				}
				if field.aggrFuc != "" {
					return false, errors.Errorf("unsupported: aggregation.in.having.clause")
				}

				for _, tb := range field.info.referTables {
					if isContainKey(tuple.referTables, tb) {
						continue
					}
					tuple.referTables = append(tuple.referTables, tb)
				}
			case *sqlparser.FuncExpr:
				if node.IsAggregate() {
					buf := sqlparser.NewTrackedBuffer(nil)
					node.Format(buf)
					return false, errors.Errorf("unsupported: expr[%s].in.having.clause", buf.String())
				}
			}
			return true, nil
		}, filter)
		if err != nil {
			return nil, err
		}

		tuples = append(tuples, tuple)
	}

	return tuples, nil
}

// getTbsInExpr used to get the referred tables from the expr.
func getTbsInExpr(expr sqlparser.Expr) []string {
	var referTables []string
	sqlparser.Walk(func(node sqlparser.SQLNode) (kontinue bool, err error) {
		switch node := node.(type) {
		case *sqlparser.ColName:
			tableName := node.Qualifier.Name.String()
			if isContainKey(referTables, tableName) {
				return true, nil
			}
			referTables = append(referTables, tableName)
		}
		return true, nil
	}, expr)
	return referTables
}

// replaceCol replace the info.cols based on the colMap.
// eg:
//  select b from (select a+1 as tmp,b from t1)t where tmp > 1;
// If want to replace `tmp>1` with the fields in subquery.
// The `colMap` is built from `a+1 as tmp` and `b`.
// The `info` is built from `tmp>1`.
// We need find and replace the cols from colMap.
// Finally, `tmp > 1` will be overwritten as `a+1 > 1`.
func replaceCol(info exprInfo, colMap map[string]selectTuple) (exprInfo, error) {
	var tables []string
	var columns []*sqlparser.ColName
	for _, old := range info.cols {
		new, err := getMatchedField(old.Name.String(), colMap)
		if err != nil {
			return info, err
		}
		if new.aggrFuc != "" {
			return info, errors.New("unsupported: aggregation.field.in.subquery.is.used.in.clause")
		}

		info.expr = sqlparser.ReplaceExpr(info.expr, old, new.info.expr)
		columns = append(columns, new.info.cols...)
		for _, referTable := range new.info.referTables {
			if !isContainKey(tables, referTable) {
				tables = append(tables, referTable)
			}
		}
	}
	info.cols = columns
	info.referTables = tables
	return info, nil
}
