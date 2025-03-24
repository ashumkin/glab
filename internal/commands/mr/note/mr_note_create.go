package note

import (
	"fmt"

	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/utils"

	"github.com/spf13/cobra"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

func NewCmdNote(f cmdutils.Factory) *cobra.Command {
	mrCreateNoteCmd := &cobra.Command{
		Use:     "note [<id> | <branch>]",
		Aliases: []string{"comment"},
		Short:   "Add a comment or note to a merge request.",
		Long:    ``,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			apiClient, err := f.HttpClient()
			if err != nil {
				return err
			}

			mr, repo, err := mrutils.MRFromArgs(f, args, "any")
			if err != nil {
				return err
			}

			body, _ := cmd.Flags().GetString("message")

			if body == "" {
				editor, err := cmdutils.GetEditor(f.Config)
				if err != nil {
					return err
				}

				body = utils.Editor(utils.EditorOptions{
					Label:         "Note message:",
					Help:          "Enter the note message for the merge request. ",
					FileName:      "*_MR_NOTE_EDITMSG.md",
					EditorCommand: editor,
				})
			}
			if body == "" {
				return fmt.Errorf("aborted... Note has an empty message.")
			}

			isThread, _ := cmd.Flags().GetBool("thread")
			if err != nil {
				return err
			}
			uniqueNoteEnabled, _ := cmd.Flags().GetBool("unique")

			if uniqueNoteEnabled {
				opts := &gitlab.ListMergeRequestNotesOptions{ListOptions: gitlab.ListOptions{PerPage: api.DefaultListLimit}}
				notes, _, err := apiClient.Notes.ListMergeRequestNotes(repo.FullName(), mr.IID, opts)
				if err != nil {
					return fmt.Errorf("running merge request note deduplication: %v", err)
				}
				for _, noteInfo := range notes {
					if noteInfo.Body == body {
						printNoteURL(f, mr.WebURL, noteInfo.ID)

						return nil
					}
				}
			}
			if isThread {
				thread, err := api.StartMRThread(apiClient, repo.FullName(), mr.IID, &gitlab.CreateMergeRequestDiscussionOptions{
					Body: &body,
				})
				if err != nil {
					return err
				}
				if len(thread.Notes) == 0 {
					return fmt.Errorf("no notes found for created discussion!? (%s)", thread.ID)
				}
				noteInfo := thread.Notes[0]
				printNoteURL(f, mr.WebURL, noteInfo.ID)

				return nil
			}
			noteInfo, _, err := apiClient.Notes.CreateMergeRequestNote(repo.FullName(), mr.IID, &gitlab.CreateMergeRequestNoteOptions{Body: &body})
			if err != nil {
				return err
			}
			printNoteURL(f, mr.WebURL, noteInfo.ID)

			return nil
		},
	}

	mrCreateNoteCmd.Flags().StringP("message", "m", "", "Comment or note message.")
	mrCreateNoteCmd.Flags().Bool("unique", false, "Don't create a comment or note if it already exists.")
	mrCreateNoteCmd.Flags().BoolP("thread", "T", false, "Add a discussion thread rather than a comment")
	return mrCreateNoteCmd
}

func printNoteURL(f cmdutils.Factory, webURL string, noteID int) {
	fmt.Fprintf(f.IO().StdOut, "%s#note_%d\n", webURL, noteID)
}
