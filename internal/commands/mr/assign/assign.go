package assign

import (
	"context"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	factory cmdutils.Factory
	io      *iostreams.IOStreams

	args     []string
	dryRun   bool
	users    []string
	groups   []string
	projects []string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{factory: f, io: f.IO()}
	cmd := &cobra.Command{
		Use:   "assign [<id | branch | url>]",
		Short: "Assign a merge request based on workload.",
		Long: heredoc.Doc(`
			Fill empty assignee and reviewer roles with users who have the fewest open, non-draft merge requests. Replace an assignee who is the merge request author.

			Candidates come from the assignees and reviewers of open, non-draft merge requests in the selected groups and projects, plus usernames passed with --users. Every candidate is eligible for either role. Prefix a username with - to exclude that user from selection and replace their existing assignments on the target merge request. Without a scope flag, use the target project. MRs assigned to their author do not count toward workload.

			Use --dry-run to preview current and proposed assignments without updating the merge request. A later run may choose different users when workload counts tie.
		`),
		Example: heredoc.Doc(`
			# Assign the merge request for the current branch using the current project
			glab mr assign

			# Include a group and additional candidates
			glab mr assign 42 --group my-group --users alice,bob,carol

			# Exclude a user from selection and replace their existing assignments
			glab mr assign 42 --users=-alice

			# Preview assignments without changing the merge request
			glab mr assign 42 --dry-run`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.args = args
			return opts.run(cmd.Context())
		},
	}
	cmdutils.EnableRepoOverride(cmd, f)
	fl := cmd.Flags()
	fl.BoolVar(&opts.dryRun, "dry-run", false, "Preview assignments without updating the merge request.")
	fl.StringSliceVarP(&opts.users, "users", "u", nil, "Add candidate <username> for either role, or prefix with - to exclude one. Multiple usernames can be comma-separated or specified by repeating the flag.")
	fl.StringSliceVarP(&opts.groups, "group", "g", nil, "Include merge requests from group <path>. Multiple groups can be comma-separated or specified by repeating the flag.")
	fl.StringSliceVar(&opts.projects, "project", nil, "Include merge requests from project <path>. Multiple projects can be comma-separated or specified by repeating the flag.")
	return cmd
}

func (o *options) run(ctx context.Context) error {
	mr, repo, err := mrutils.MRFromArgs(ctx, o.factory, o.args, "any")
	if err != nil {
		return err
	}
	needsAssignee, needsReviewer, _, _ := rolesNeeded(mr, nil)
	hasExclusions := false
	for _, user := range o.users {
		if strings.HasPrefix(user, "-") {
			hasExclusions = true
			break
		}
	}
	if needsAssignee == 0 && needsReviewer == 0 && !hasExclusions {
		if o.dryRun {
			writeDryRunPreview(o.io, mr, &gitlab.UpdateMergeRequestOptions{}, nil, nil)
			return nil
		}
		writeAuthor(o.io, mr.Author)
		o.io.LogInfof("Merge request !%d already has an assignee and reviewer.\n", mr.IID)
		return nil
	}
	client, err := o.factory.GitLabClient()
	if err != nil {
		return err
	}
	baseRepo, err := o.factory.BaseRepo()
	if err != nil {
		return err
	}
	if repo.RepoHost() != baseRepo.RepoHost() {
		apiClient, err := o.factory.ApiClient(repo.RepoHost())
		if err != nil {
			return err
		}
		client = apiClient.Lab()
	}
	candidateIDs, excludedIDs, usernames, err := userIDs(ctx, client, o.users)
	if err != nil {
		return fmt.Errorf("user candidates: %w", err)
	}
	projects := o.projects
	if len(o.groups) == 0 && len(projects) == 0 {
		projects = []string{repo.FullName()}
	}
	var mrs []*gitlab.BasicMergeRequest
	for _, group := range o.groups {
		if group == "" {
			return &cmdutils.FlagError{Err: fmt.Errorf("--group requires a non-empty path")}
		}
		opts := &gitlab.ListGroupMergeRequestsOptions{State: new("opened"), Draft: new(false)}
		found, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			return client.MergeRequests.ListGroupMergeRequests(group, opts, p, gitlab.WithContext(ctx))
		})
		if err != nil {
			return fmt.Errorf("list group %s merge requests: %w", group, err)
		}
		mrs = append(mrs, found...)
	}
	for _, project := range projects {
		if project == "" {
			return &cmdutils.FlagError{Err: fmt.Errorf("--project requires a non-empty path")}
		}
		opts := &gitlab.ListProjectMergeRequestsOptions{State: new("opened"), Draft: new(false)}
		found, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			return client.MergeRequests.ListProjectMergeRequests(project, opts, p, gitlab.WithContext(ctx))
		})
		if err != nil {
			return fmt.Errorf("list project %s merge requests: %w", project, err)
		}
		mrs = append(mrs, found...)
	}
	update, err := assignmentOptions(mr, mrs, candidateIDs, excludedIDs)
	if err != nil {
		return err
	}
	if update.AssigneeIDs == nil && update.ReviewerIDs == nil {
		if o.dryRun {
			writeDryRunPreview(o.io, mr, update, nil, usernames)
			return nil
		}
		writeAuthor(o.io, mr.Author)
		o.io.LogInfof("Merge request !%d already has an assignee and reviewer.\n", mr.IID)
		return nil
	}
	if o.dryRun {
		var users []*gitlab.BasicUser
		for _, source := range mrs {
			if source == nil {
				continue
			}
			users = append(users, source.Assignees...)
			if source.Assignee != nil {
				users = append(users, source.Assignee)
			}
			users = append(users, source.Reviewers...)
		}
		writeDryRunPreview(o.io, mr, update, users, usernames)
		return nil
	}
	_, _, err = client.MergeRequests.UpdateMergeRequest(repo.FullName(), mr.IID, update, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("update merge request !%d: %w", mr.IID, err)
	}
	writeAuthor(o.io, mr.Author)
	o.io.LogInfof("Updated merge request !%d assignments.\n", mr.IID)
	return nil
}

func userIDs(ctx context.Context, client *gitlab.Client, usernames []string) ([]int64, map[int64]struct{}, map[int64]string, error) {
	ids := make([]int64, 0, len(usernames))
	included := make(map[int64]struct{}, len(usernames))
	excluded := make(map[int64]struct{})
	names := make(map[int64]string, len(usernames))
	resolved := make(map[string]*gitlab.User, len(usernames))
	for _, entry := range usernames {
		isExcluded := strings.HasPrefix(entry, "-")
		username := strings.TrimPrefix(entry, "-")
		if username == "" {
			return nil, nil, nil, &cmdutils.FlagError{Err: fmt.Errorf("--users requires a username after -")}
		}
		user, ok := resolved[username]
		if ok && user == nil {
			if isExcluded {
				continue
			}
			return nil, nil, nil, fmt.Errorf("failed to find user by name: %s", username)
		}
		if !ok {
			var err error
			if isExcluded && username != "@me" {
				found, _, lookupErr := client.Users.ListUsers(&gitlab.ListUsersOptions{Username: new(username)}, gitlab.WithContext(ctx))
				if lookupErr != nil {
					return nil, nil, nil, lookupErr
				}
				if len(found) == 0 {
					resolved[username] = nil
					continue
				}
				if len(found) != 1 {
					return nil, nil, nil, fmt.Errorf("failed to find user by name: %s", username)
				}
				user = found[0]
			} else {
				user, err = api.UserByName(client, username, gitlab.WithContext(ctx))
				if err != nil {
					return nil, nil, nil, err
				}
			}
			resolved[username] = user
		}
		if isExcluded {
			excluded[user.ID] = struct{}{}
		} else if _, ok := included[user.ID]; !ok {
			ids = append(ids, user.ID)
			included[user.ID] = struct{}{}
		}
		names[user.ID] = user.Username
	}
	return ids, excluded, names, nil
}

func writeAuthor(io *iostreams.IOStreams, author *gitlab.BasicUser) {
	name := "(unknown)"
	if author != nil {
		name = fmt.Sprintf("ID %d", author.ID)
		if author.Username != "" {
			name = "@" + author.Username
		}
	}
	io.LogInfof("Author: %s\n", name)
}

func writeDryRunPreview(io *iostreams.IOStreams, mr *gitlab.MergeRequest, update *gitlab.UpdateMergeRequestOptions, users []*gitlab.BasicUser, names map[int64]string) {
	if names == nil {
		names = make(map[int64]string)
	}
	addNames := func(users []*gitlab.BasicUser) {
		for _, user := range users {
			if user != nil && user.Username != "" {
				names[user.ID] = user.Username
			}
		}
	}
	addNames(mr.Assignees)
	if mr.Assignee != nil {
		addNames([]*gitlab.BasicUser{mr.Assignee})
	}
	addNames(mr.Reviewers)
	addNames(users)

	currentAssignees := make([]int64, 0, len(mr.Assignees))
	for _, user := range mr.Assignees {
		if user != nil {
			currentAssignees = append(currentAssignees, user.ID)
		}
	}
	if len(mr.Assignees) == 0 && mr.Assignee != nil {
		currentAssignees = append(currentAssignees, mr.Assignee.ID)
	}
	currentReviewers := make([]int64, 0, len(mr.Reviewers))
	for _, user := range mr.Reviewers {
		if user != nil {
			currentReviewers = append(currentReviewers, user.ID)
		}
	}
	proposedAssignees := currentAssignees
	if update.AssigneeIDs != nil {
		proposedAssignees = *update.AssigneeIDs
	}
	proposedReviewers := currentReviewers
	if update.ReviewerIDs != nil {
		proposedReviewers = *update.ReviewerIDs
	}
	format := func(ids []int64) string {
		if len(ids) == 0 {
			return "(none)"
		}
		users := make([]string, 0, len(ids))
		for _, id := range ids {
			if name := names[id]; name != "" {
				users = append(users, "@"+name)
			} else {
				users = append(users, fmt.Sprintf("ID %d", id))
			}
		}
		return strings.Join(users, ", ")
	}
	io.LogInfof("Dry run for merge request !%d:\n", mr.IID)
	writeAuthor(io, mr.Author)
	io.LogInfof("Assignees: %s -> %s\n", format(currentAssignees), format(proposedAssignees))
	io.LogInfof("Reviewers: %s -> %s\n", format(currentReviewers), format(proposedReviewers))
	io.LogInfo("No changes were made.")
}
