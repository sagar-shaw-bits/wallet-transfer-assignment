package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTransferResultIsFailed(t *testing.T) {
	t.Run("failed state", func(t *testing.T) {
		result := TransferResult{State: TransferStateFailed}
		assert.True(t, result.IsFailed(), "failed state should be considered failed")
	})

	t.Run("error message", func(t *testing.T) {
		result := TransferResult{Error: "insufficient_funds"}
		assert.True(t, result.IsFailed(), "error should mark result as failed")
	})

	t.Run("successful state", func(t *testing.T) {
		result := TransferResult{State: TransferStateProcessed}
		assert.False(t, result.IsFailed(), "processed state should not be treated as failed")
	})
}
