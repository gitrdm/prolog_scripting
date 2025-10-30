package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	t.Run("go_string", func(t *testing.T) {
		p := New(nil, nil)
		assert.NoError(t, p.QuerySolution(`go_string("foo", '"foo"').`).Err())
	})
}

func TestUserInput_Write(t *testing.T) {
	u := &userInput{}
	n, err := u.Write([]byte("test"))
	assert.Equal(t, 0, n)
	assert.NoError(t, err)
}

// TestUserInput_Read is commented out because it requires mocking the terminal,
// which is complex. The Read method is exercised in integration tests.
// func TestUserInput_Read(t *testing.T) {
// 	// TODO: Mock terminal for proper testing.
// }
