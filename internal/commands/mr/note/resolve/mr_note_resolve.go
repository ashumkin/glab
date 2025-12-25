package noteresolve

import (
	"fmt"
	"slices"
	"strconv"
	"sync"

	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"

	"github.com/spf13/cobra"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
)

type Options struct {
	all     bool
	mrID    int
	message string
}

func NewCmdNote(f cmdutils.Factory) *cobra.Command {
	opts := Options{}
	mrResolveNoteCmd := &cobra.Command{
		Use:     "resolve [-m MRID] <id>",
		Aliases: []string{""},
		Short:   "Resolves a thread to a merge request.",
		Long:    ``,
		Args:    cobra.MinimumNArgs(0),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			m, repo, err := mrutils.MRFromArgs(f, args, "opened")
			if err != nil {
				return err
			}

			ids := make(map[string]int)
			discussions, _, err := client.Discussions.ListMergeRequestDiscussions(repo.FullName(), m.IID,
				&gitlab.ListMergeRequestDiscussionsOptions{PerPage: 1000})
			if err != nil {
				return fmt.Errorf("error fetching MR discussions: %w", err)
			}
			if opts.all {
				for _, discussion := range discussions {
					for _, note := range discussion.Notes {
						if !note.Resolvable && !note.Resolved {
							continue
						}
						if _, ok := ids[discussion.ID]; !ok {
							ids[discussion.ID] = note.ID
						}
					}
				}
			} else if len(args) == 0 {
				err := cmd.Usage()
				if err != nil {
					return err
				}
				return nil
			} else {
				var noteIDs []int
				for _, a := range args {
					i, err := strconv.Atoi(a)
					if err != nil {
						return fmt.Errorf("error parsing note ID (%s): %w", a, err)
					}
					noteIDs = append(noteIDs, i)
				}
				for _, discussion := range discussions {
					for _, note := range discussion.Notes {
						if !note.Resolvable || !note.Resolved {
							continue
						}
						if !slices.Contains(noteIDs, note.ID) {
							continue
						}
						if _, ok := ids[discussion.ID]; !ok {
							ids[discussion.ID] = note.ID
						}
					}
				}
			}
			if len(ids) == 0 {
				return fmt.Errorf("no unresolved threads were found")
			}
			g := sync.WaitGroup{}
			for id, note := range ids {
				g.Go(func() {
					if opts.message == "" {
						fmt.Fprintf(f.IO().StdOut, "Resolving %s, note %d...\n", id, note)
					} else {
						fmt.Fprintf(f.IO().StdOut, "Resolving %s, note %d with a message: %s...\n", id, note, opts.message)
						_, _, err = client.Discussions.AddMergeRequestDiscussionNote(repo.FullName(), m.IID, id,
							&gitlab.AddMergeRequestDiscussionNoteOptions{Body: gitlab.Ptr(opts.message)})
						if err != nil {
							fmt.Fprintf(f.IO().StdOut, "error making a comment to %s, note %d: %s\n", id, note, err)
							return
						}
					}

					_, _, err := client.Discussions.ResolveMergeRequestDiscussion(repo.FullName(), m.IID, id,
						&gitlab.ResolveMergeRequestDiscussionOptions{Resolved: gitlab.Ptr(true)})
					if err != nil {
						fmt.Fprintf(f.IO().StdOut, "error resolving %s: %d: %s\n", id, note, err)
						return
					}
					fmt.Fprintf(f.IO().StdOut, "%s#note_%d\n", m.WebURL, note)
				})
			}
			g.Wait()

			return nil
		},
	}

	mrResolveNoteCmd.Flags().BoolVarP(&opts.all, "all", "A", false, "Resolve all unresolved threads")
	mrResolveNoteCmd.Flags().IntVarP(&opts.mrID, "mr", "M", -1, "MR ID")
	mrResolveNoteCmd.Flags().StringVarP(&opts.message, "message", "m", "", "Comment or note message.")
	return mrResolveNoteCmd
}
