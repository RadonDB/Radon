/*
 * Radon
 *
 * Copyright 2018 The Radon Authors.
 * Code is licensed under the GPLv3.
 *
 */

package planner

import (
	"xcontext"

	"github.com/xelabs/go-mysqlstack/sqlparser"
)

// PlanNode interface.
type PlanNode interface {
	buildQuery(tbInfos map[string]*tableInfo)
	Children() *PlanTree
	getFields() []selectTuple
	getReferTables() map[string]*tableInfo
	GetQuery() []xcontext.QueryTuple
	pushOrderBy(sel sqlparser.SelectStatement) error
	pushLimit(sel sqlparser.SelectStatement) error
}

// SelectNode interface.
type SelectNode interface {
	PlanNode
	pushFilter(filters []exprInfo) error
	setParent(p SelectNode)
	setWhereFilter(filter exprInfo)
	setNoTableFilter(exprs []sqlparser.Expr)
	setParenthese(hasParen bool)
	pushEqualCmpr(joins []exprInfo) SelectNode
	calcRoute() (SelectNode, error)
	pushSelectExprs(fields, groups []selectTuple, sel *sqlparser.Select, aggTyp aggrType) error
	pushSelectExpr(field selectTuple) (int, error)
	pushHaving(havings []exprInfo) error
	pushMisc(sel *sqlparser.Select)
	reOrder(int)
	Order() int
}

// findLCA get the two plannode's lowest common ancestors node.
func findLCA(h, p1, p2 SelectNode) SelectNode {
	if p1 == h || p2 == h {
		return h
	}
	jn, ok := h.(*JoinNode)
	if !ok {
		return nil
	}
	pl := findLCA(jn.Left, p1, p2)
	pr := findLCA(jn.Right, p1, p2)

	if pl != nil && pr != nil {
		return jn
	}
	if pl == nil {
		return pr
	}
	return pl
}

// getOneTableInfo get a tableInfo.
func getOneTableInfo(tbInfos map[string]*tableInfo) (string, *tableInfo) {
	for tb, tbInfo := range tbInfos {
		return tb, tbInfo
	}
	return "", nil
}

// procure requests for the specified column from the plan
// and returns the join var name for it.
func procure(tbInfos map[string]*tableInfo, col *sqlparser.ColName) string {
	var joinVar string
	field := col.Name.String()
	table := col.Qualifier.Name.String()
	tbInfo := tbInfos[table]
	node := tbInfo.parent
	jn := node.parent.(*JoinNode)

	joinVar = col.Qualifier.Name.CompliantName() + "_" + col.Name.CompliantName()
	if _, ok := jn.Vars[joinVar]; ok {
		return joinVar
	}

	tuples := node.getFields()
	index := -1
	for i, tuple := range tuples {
		if tuple.isCol {
			if field == tuple.field && table == tuple.referTables[0] {
				index = i
				break
			}
		}
	}
	// key not in the select fields.
	if index == -1 {
		tuple := selectTuple{
			expr:        &sqlparser.AliasedExpr{Expr: col},
			field:       field,
			referTables: []string{table},
		}
		index, _ = node.pushSelectExpr(tuple)
	}

	jn.Vars[joinVar] = index
	return joinVar
}
