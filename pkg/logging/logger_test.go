package logging_test

import (
	"testing"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestWithDefaultLogLevel(t *testing.T) {
	// Arrange.
	t.Parallel()

	// Act.
	logger := logging.NewLogger()

	// Assert.
	assert.Equal(t, logging.DefaultLevel, logger.Level(), "should set the log level to the default value")
	assert.Equal(t, zap.WarnLevel, logger.Level(), "should default to the warn log level")
}

func TestWithCustomLogLevel(t *testing.T) {
	// Arrange.
	t.Setenv("LOG_LEVEL", "debug")

	// Act.
	logger := logging.NewLogger()

	// Assert.
	assert.Equal(t, zap.DebugLevel, logger.Level(), "should set the log level to debug")
}

func TestWithInvalidLogLevel(t *testing.T) {
	// Arrange.
	t.Setenv("LOG_LEVEL", "invalid")

	// Act.
	logger := logging.NewLogger()

	// Assert.
	assert.Equal(t, logging.DefaultLevel, logger.Level(), "should fall back to the default log level on invalid input")
}
