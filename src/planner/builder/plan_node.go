/*
 * Radon
 *
 * Copyright 2018 The Radon Authors.
 * Code is licensed under the GPLv3.
 *
 */

package builder

import (
	"xcontext"

	"github.com/pkg/errors"
	"github.com/xelabs/go-mysqlstack/sqlparser"
)

// PlanNode interface.
type PlanNode interface {
	addNoTableFilter(exprs []sqlparser.Expr)
	buildQuery(root PlanNode)
	calcRoute() (PlanNode, error)
	Children() []ChildPlan
	getFields() []selectTuple
	getReferTables() map[string]*tableInfo
	GetQuery() []xcontext.QueryTuple
	pushSelectExprs(fields, groups []selectTuple, sel *sqlparser.Select, aggTyp aggrType) error
	pushSelectExpr(field selectTuple) (int, error)
	pushFilter(filter exprInfo) error
	pushKeyFilter(filter exprInfo, table, field string) error
	pushHaving(having exprInfo) error
	pushOrderBy(orderBy sqlparser.OrderBy) error
	pushLimit(limit *sqlparser.Limit) error
	pushMisc(sel *sqlparser.Select)
	reOrder(int)
	setParent(p *JoinNode)
	Order() int
}

// findLCA get the two plannode's lowest common ancestors node.
func findLCA(h, p1, p2 PlanNode) PlanNode {
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

// setParenthese is only used in JoinNode and MergeNode.
func setParenthese(node PlanNode, hasParen bool) {
	switch node := node.(type) {
	case *JoinNode:
		node.hasParen = hasParen
	case *MergeNode:
		node.hasParen = hasParen
	}
}

func findParent(tables []string, node PlanNode) PlanNode {
	var parent PlanNode
	for _, tb := range tables {
		tbInfo := node.getReferTables()[tb]
		if parent == nil {
			parent = tbInfo.parent
			continue
		}
		if parent != tbInfo.parent {
			parent = findLCA(node, parent, tbInfo.parent)
		}
	}
	return parent
}

func addFilter(s PlanNode, filter exprInfo) error {
	switch node := s.(type) {
	case *JoinNode:
		node.otherFilter = append(node.otherFilter, filter)
	case *MergeNode:
		node.addWhere(filter.expr)
	case *SubNode:
		return node.pushFilter(filter)
	}
	return nil
}

// pushFilters push a WHERE clause down, and update the PlanNode info.
func pushFilters(s PlanNode, expr sqlparser.Expr) (PlanNode, error) {
	joins, filters, err := parseWhereOrJoinExprs(expr, s.getReferTables())
	if err != nil {
		return s, err
	}

	for _, filter := range filters {
		if err := s.pushFilter(filter); err != nil {
			return s, err
		}
	}

	switch node := s.(type) {
	case *MergeNode:
		for _, joinCond := range joins {
			node.addWhere(joinCond.expr)
		}
	case *JoinNode:
		return node.pushEqualCmprs(joins), nil
	}
	return s, nil
}

// pushHavings push a HAVING clause down.
func pushHavings(s PlanNode, expr sqlparser.Expr, tbInfos map[string]*tableInfo) error {
	havings, err := parseHaving(expr, tbInfos, s.getFields())
	if err != nil {
		return err
	}
	for _, having := range havings {
		if err = s.pushHaving(having); err != nil {
			return err
		}
	}
	return nil
}

func handleSelectExpr(field selectTuple, node PlanNode) (int, error) {
	parent := findParent(field.info.referTables, node)
	if j, ok := parent.(*JoinNode); ok {
		if j.Strategy != NestLoop {
			return -1, errors.Errorf("unsupported: '%s'.expression.in.cross-shard.query", field.alias)
		}
	}
	return parent.pushSelectExpr(field)
}

func handleFilter(filter exprInfo, node PlanNode) error {
	parent := findParent(filter.referTables, node)
	if j, ok := parent.(*JoinNode); ok {
		if j.Strategy == NestLoop {
			return j.pushOtherFilters([]exprInfo{filter}, false)
		}

		buf := sqlparser.NewTrackedBuffer(nil)
		filter.expr.Format(buf)
		return errors.Errorf("unsupported: where.clause.'%s'.in.cross-shard.join", buf.String())
	}
	return parent.pushFilter(filter)
}

func handleHaving(having exprInfo, node PlanNode) error {
	parent := findParent(having.referTables, node)
	if j, ok := parent.(*JoinNode); ok {
		if j.Strategy != NestLoop {
			buf := sqlparser.NewTrackedBuffer(nil)
			having.expr.Format(buf)
			return errors.Errorf("unsupported: having.clause.'%s'.in.cross-shard.join", buf.String())
		}
	}
	return parent.pushHaving(having)
}
