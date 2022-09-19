package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/cli/pkg/iostreams"

	"github.com/spf13/cobra/doc"
	"github.com/spf13/pflag"

	"github.com/spf13/cobra"
	"gitlab.com/gitlab-org/cli/commands"
	"gitlab.com/gitlab-org/cli/commands/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/pkg/utils"
)

var tocTree = `.. toctree::
   :glob:
   :maxdepth: 0

%s

`

func main() {
	var flagErr pflag.ErrorHandling
	docsCmd := pflag.NewFlagSet("", flagErr)
	manpage := docsCmd.BoolP("manpage", "m", false, "Generate manual pages instead of web docs")
	path := docsCmd.StringP("path", "p", "./docs/source/", "Path where you want the generated docs saved")
	help := docsCmd.BoolP("help", "h", false, "Help about any command")

	if err := docsCmd.Parse(os.Args); err != nil {
		fatal(err)
	}
	if *help {
		_, err := fmt.Fprintf(os.Stderr, "Usage of %s:\n\n%s", os.Args[0], docsCmd.FlagUsages())
		if err != nil {
			fatal(err)
		}
		os.Exit(1)
	}
	err := os.MkdirAll(*path, 0755)
	if err != nil {
		fatal(err)
	}

	ioStream, _, _, _ := iostreams.Test()
	glabCli := commands.NewCmdRoot(&cmdutils.Factory{IO: ioStream}, "", "")
	glabCli.DisableAutoGenTag = true
	if *manpage {
		if err := genManPage(glabCli, *path); err != nil {
			fatal(err)
		}
	} else {
		if err := genWebDocs(glabCli, *path); err != nil {
			fatal(err)
		}
	}
}

func genManPage(glabCli *cobra.Command, path string) error {
	header := &doc.GenManHeader{
		Title:   "glab",
		Section: "1",
		Source:  "",
		Manual:  "",
	}
	err := doc.GenManTree(glabCli, header, path)
	if err != nil {
		return err
	}
	return nil
}

func genWebDocs(glabCli *cobra.Command, path string) error {
	cmds := glabCli.Commands()

	for _, cmd := range cmds {
		fmt.Println("Generating docs for " + cmd.Name())
		// create directories for parent commands
		_ = os.MkdirAll(path+cmd.Name(), 0750)

		// Generate parent command
		out := new(bytes.Buffer)
		err := GenReSTCustom(cmd, out)
		if err != nil {
			return err
		}

		// Generate children commands
		for _, cmdC := range cmd.Commands() {
			err = GenReSTTreeCustom(cmdC, path+cmd.Name())
			if err != nil {
				return err
			}
		}

		err = config.WriteFile(path+cmd.Name()+"/index.rst", out.Bytes(), 0755)
		if err != nil {
			return err
		}
	}
	return nil
}

func printSubcommands(cmd *cobra.Command, buf *bytes.Buffer) {
	if len(cmd.Commands()) < 1 {
		return
	}

	var tree string
	// Generate children commands
	for _, cmdC := range cmd.Commands() {
		if cmdC.Name() != "help" {
			tree += fmt.Sprintf("%s <%s>\n", cmdC.Name(), cmdC.Name())
		}
	}

	subcommands := ""
	if tree != "" {
		tree = utils.Indent(tree, "   ")
		subcommands = fmt.Sprintf(tocTree, tree)
	}

	if subcommands != "" {
		buf.WriteString("Subcommands\n")
		buf.WriteString("~~~~~~~~~~~\n")
		buf.WriteString(subcommands)
		buf.WriteString("\n")
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// adapted from : github.com/spf13/cobra/doc/rest_docs.go

// GenReSTTreeCustom is the the same as GenReSTTree, but
// with custom filePrepender and linkHandler.
func GenReSTTreeCustom(cmd *cobra.Command, dir string) error {
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err := GenReSTTreeCustom(c, dir); err != nil {
			return err
		}
	}

	basename := cmd.Name() + ".rst"
	filename := filepath.Join(dir, basename)
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := GenReSTCustom(cmd, f); err != nil {
		return err
	}
	return nil
}

// GenReSTCustom creates custom reStructured Text output.
func GenReSTCustom(cmd *cobra.Command, w io.Writer) error {
	cmd.InitDefaultHelpCmd()
	cmd.InitDefaultHelpFlag()

	buf := new(bytes.Buffer)
	name := cmd.CommandPath()

	short := cmd.Short
	long := cmd.Long
	if long == "" {
		long = short
	}
	ref := strings.ReplaceAll(name, " ", "_")

	buf.WriteString(".. _" + ref + ":\n\n")
	buf.WriteString(name + "\n")
	buf.WriteString(strings.Repeat("-", len(name)) + "\n\n")
	buf.WriteString(short + "\n\n")
	buf.WriteString("Synopsis\n")
	buf.WriteString("~~~~~~~~\n\n")
	buf.WriteString("\n" + long + "\n\n")

	if cmd.Runnable() {
		buf.WriteString(fmt.Sprintf("::\n\n  %s\n\n", cmd.UseLine()))
	}

	if len(cmd.Example) > 0 {
		buf.WriteString("Examples\n")
		buf.WriteString("~~~~~~~~\n\n")
		buf.WriteString(fmt.Sprintf("::\n\n%s\n\n", utils.Indent(cmd.Example, "  ")))
	}

	if err := printOptionsReST(buf, cmd, name); err != nil {
		return err
	}

	printSubcommands(cmd, buf)

	_, err := buf.WriteTo(w)
	return err
}

// adapted from : github.com/spf13/cobra/doc/rest_docs.go
func printOptionsReST(buf *bytes.Buffer, cmd *cobra.Command, name string) error {
	flags := cmd.NonInheritedFlags()
	flags.SetOutput(buf)
	if flags.HasAvailableFlags() {
		buf.WriteString("Options\n")
		buf.WriteString("~~~~~~~\n\n::\n\n")
		flags.PrintDefaults()
		buf.WriteString("\n")
	}

	parentFlags := cmd.InheritedFlags()
	parentFlags.SetOutput(buf)
	if parentFlags.HasAvailableFlags() {
		buf.WriteString("Options inherited from parent commands\n")
		buf.WriteString("~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\n\n::\n\n")
		parentFlags.PrintDefaults()
		buf.WriteString("\n")
	}
	return nil
}
