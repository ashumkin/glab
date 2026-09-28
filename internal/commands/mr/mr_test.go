//go:build !integration

package mr

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMain(m *testing.M) {
	cmdtest.InitTest(m, "mr_cmd_test")
	cmdtest.InitTest(m, "mr_cmd_autofill")
}

func TestMrCmd_noARgs(t *testing.T) {
	var buf bytes.Buffer
	cmd := NewCmdMR(cmdtest.NewTestFactory(nil))
	cmd.SetOut(&buf)

	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "Use \"mr [command] --help\" for more information about a command.\n")
}

func TestMrCmdHasAssign(t *testing.T) {
	t.Parallel()

	cmd := NewCmdMR(cmdtest.NewTestFactory(nil))
	assign, _, err := cmd.Find([]string{"assign"})
	require.NoError(t, err)
	assert.Equal(t, "assign", assign.Name())
}
