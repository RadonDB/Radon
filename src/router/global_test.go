/*
 * Radon
 *
 * Copyright 2018 The Radon Authors.
 * Code is licensed under the GPLv3.
 *
 */

package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xelabs/go-mysqlstack/xlog"
)

func TestGlobal(t *testing.T) {
	log := xlog.NewStdLog(xlog.Level(xlog.PANIC))
	global := NewGlobal(log, MockTableGConfig())
	{
		err := global.Build()
		assert.Nil(t, err)
		assert.Equal(t, string(global.Type()), methodTypeGlobal)
	}
}

func TestGlobalLookup(t *testing.T) {
	log := xlog.NewStdLog(xlog.Level(xlog.PANIC))
	global := NewGlobal(log, MockTableGConfig())
	{
		err := global.Build()
		assert.Nil(t, err)
	}

	{
		parts, err := global.Lookup(nil, nil)
		assert.Nil(t, err)
		assert.Equal(t, 2, len(parts))
	}
}
