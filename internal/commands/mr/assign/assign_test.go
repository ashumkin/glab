//go:build !integration

package assign

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestAssignmentOptionsExcludesSelfAssignedWork(t *testing.T) {
	t.Parallel()

	author := &gitlab.BasicUser{ID: 1, Username: "author"}
	alice := &gitlab.BasicUser{ID: 2, Username: "alice"}
	bob := &gitlab.BasicUser{ID: 3, Username: "bob"}
	carol := &gitlab.BasicUser{ID: 4, Username: "carol"}
	dan := &gitlab.BasicUser{ID: 5, Username: "dan"}
	target := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 99, IID: 9, Author: author, Assignees: []*gitlab.BasicUser{author}}}
	mrs := []*gitlab.BasicMergeRequest{
		{ID: 10, State: "opened", Author: alice, Assignees: []*gitlab.BasicUser{alice}, Reviewers: []*gitlab.BasicUser{dan}},
		{ID: 11, State: "opened", Author: alice, Assignees: []*gitlab.BasicUser{alice}, Reviewers: []*gitlab.BasicUser{dan}},
		{ID: 12, State: "opened", Author: carol, Assignees: []*gitlab.BasicUser{bob}, Reviewers: []*gitlab.BasicUser{carol}},
		{ID: 99, State: "opened", Author: author, Assignees: []*gitlab.BasicUser{author}},
	}

	opts, err := assignmentOptions(target, mrs, nil, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{2, 5}, []int64{(*opts.AssigneeIDs)[0], (*opts.ReviewerIDs)[0]})
}

func TestAssignUsesCurrentProjectAndFillsMissingRoles(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	author := &gitlab.BasicUser{ID: 1, Username: "author"}
	alice := &gitlab.BasicUser{ID: 2, Username: "alice"}
	bob := &gitlab.BasicUser{ID: 3, Username: "bob"}
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 99, IID: 9, Author: author}}, nil, nil)
	tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, opts *gitlab.ListProjectMergeRequestsOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "opened", *opts.State)
			return []*gitlab.BasicMergeRequest{{ID: 10, State: "opened", Author: author, Assignees: []*gitlab.BasicUser{alice}, Reviewers: []*gitlab.BasicUser{bob}}}, &gitlab.Response{}, nil
		})
	tc.MockMergeRequests.EXPECT().UpdateMergeRequest("OWNER/REPO", int64(9), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, _ int64, opts *gitlab.UpdateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.ElementsMatch(t, []int64{2, 3}, []int64{(*opts.AssigneeIDs)[0], (*opts.ReviewerIDs)[0]})
			return &gitlab.MergeRequest{}, nil, nil
		})
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	out, err := exec("9")
	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), "Author: @author\nUpdated merge request !9 assignments.")
}

func TestAssignmentOptionsPreservesExistingReviewerAndReplacesAuthor(t *testing.T) {
	t.Parallel()

	author := &gitlab.BasicUser{ID: 1}
	assigned := &gitlab.BasicUser{ID: 2}
	reviewer := &gitlab.BasicUser{ID: 3}
	target := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
		ID: 99, Author: author, Assignees: []*gitlab.BasicUser{author, assigned}, Reviewers: []*gitlab.BasicUser{reviewer},
	}}
	opts, err := assignmentOptions(target, nil, []int64{4}, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{2, 4}, *opts.AssigneeIDs)
	require.Nil(t, opts.ReviewerIDs)
}

func TestAssignmentOptionsReplacesSingularAuthorAlongsidePluralAssignees(t *testing.T) {
	t.Parallel()

	target := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
		Author: &gitlab.BasicUser{ID: 1}, Assignee: &gitlab.BasicUser{ID: 1},
		Assignees: []*gitlab.BasicUser{{ID: 2}}, Reviewers: []*gitlab.BasicUser{{ID: 3}},
	}}
	opts, err := assignmentOptions(target, nil, []int64{4}, nil)
	require.NoError(t, err)
	require.NotNil(t, opts.AssigneeIDs)
	require.Equal(t, []int64{2, 4}, *opts.AssigneeIDs)
}

func TestAssignmentOptionsReplacesEachExcludedAssignment(t *testing.T) {
	t.Parallel()

	target := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
		Author:    &gitlab.BasicUser{ID: 1},
		Assignees: []*gitlab.BasicUser{{ID: 2}, {ID: 3}, {ID: 5}},
		Reviewers: []*gitlab.BasicUser{{ID: 4}, {ID: 6}},
	}}
	mrs := []*gitlab.BasicMergeRequest{{ID: 10, State: "opened", Author: target.Author, Assignees: []*gitlab.BasicUser{{ID: 10}}}}
	opts, err := assignmentOptions(target, mrs, []int64{7, 8, 9}, map[int64]struct{}{2: {}, 3: {}, 4: {}})
	require.NoError(t, err)
	require.NotNil(t, opts.AssigneeIDs)
	require.NotNil(t, opts.ReviewerIDs)
	require.Len(t, *opts.AssigneeIDs, 3)
	require.Len(t, *opts.ReviewerIDs, 2)
	require.Equal(t, int64(5), (*opts.AssigneeIDs)[0])
	require.Equal(t, int64(6), (*opts.ReviewerIDs)[0])
	assert.ElementsMatch(t, []int64{7, 8, 9}, append((*opts.AssigneeIDs)[1:], (*opts.ReviewerIDs)[1:]...))
}

func TestAssignmentOptionsFailsWhenExclusionsLeaveTooFewCandidates(t *testing.T) {
	t.Parallel()

	target := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
		Author: &gitlab.BasicUser{ID: 1}, Assignees: []*gitlab.BasicUser{{ID: 2}}, Reviewers: []*gitlab.BasicUser{{ID: 3}},
	}}
	_, err := assignmentOptions(target, nil, []int64{4}, map[int64]struct{}{2: {}, 3: {}})
	require.ErrorContains(t, err, "no eligible")
}

func TestAssignmentOptionsSkipsPopulatedRoles(t *testing.T) {
	t.Parallel()

	target := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
		Author: &gitlab.BasicUser{ID: 1}, Assignees: []*gitlab.BasicUser{{ID: 2}}, Reviewers: []*gitlab.BasicUser{{ID: 3}},
	}}
	opts, err := assignmentOptions(target, nil, nil, nil)
	require.NoError(t, err)
	require.Nil(t, opts.AssigneeIDs)
	require.Nil(t, opts.ReviewerIDs)
}

func TestAssignmentOptionsRejectsNoDistinctPair(t *testing.T) {
	t.Parallel()

	target := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{Author: &gitlab.BasicUser{ID: 1}}}
	_, err := assignmentOptions(target, nil, []int64{2}, nil)
	require.ErrorContains(t, err, "no eligible")
}

func TestAssignmentOptionsDeduplicatesScopeOverlap(t *testing.T) {
	t.Parallel()

	author := &gitlab.BasicUser{ID: 1}
	alice := &gitlab.BasicUser{ID: 2}
	bob := &gitlab.BasicUser{ID: 3}
	carol := &gitlab.BasicUser{ID: 4}
	target := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 99, Author: author, Reviewers: []*gitlab.BasicUser{carol}}}
	aliceMR := &gitlab.BasicMergeRequest{ID: 10, State: "opened", Author: author, Assignees: []*gitlab.BasicUser{alice}}
	mrs := []*gitlab.BasicMergeRequest{
		aliceMR, aliceMR, aliceMR,
		{ID: 11, State: "opened", Author: author, Assignees: []*gitlab.BasicUser{bob}},
		{ID: 12, State: "opened", Author: author, Assignees: []*gitlab.BasicUser{bob}},
	}
	opts, err := assignmentOptions(target, mrs, nil, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, *opts.AssigneeIDs)
}

func TestAssignmentOptionsIgnoresDraftAndTargetCandidates(t *testing.T) {
	t.Parallel()

	target := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
		ID: 99, Author: &gitlab.BasicUser{ID: 1}, Reviewers: []*gitlab.BasicUser{{ID: 5}},
	}}
	mrs := []*gitlab.BasicMergeRequest{
		{ID: 10, State: "opened", Draft: true, Assignees: []*gitlab.BasicUser{{ID: 2}}},
		{ID: 99, State: "opened", Assignees: []*gitlab.BasicUser{{ID: 3}}},
		{ID: 11, State: "opened", Assignees: []*gitlab.BasicUser{{ID: 4}}},
	}
	opts, err := assignmentOptions(target, mrs, nil, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{4}, *opts.AssigneeIDs)
}

func TestAssignUsesExplicitCandidatesAndScopes(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 99, IID: 9, Author: &gitlab.BasicUser{ID: 1}}}, nil, nil)
	tc.MockUsers.EXPECT().ListUsers(gomock.Any(), gomock.Any()).DoAndReturn(func(opts *gitlab.ListUsersOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.User, *gitlab.Response, error) {
		assert.Equal(t, "alice", *opts.Username)
		return []*gitlab.User{{ID: 2, Username: "alice"}}, nil, nil
	})
	tc.MockUsers.EXPECT().ListUsers(gomock.Any(), gomock.Any()).DoAndReturn(func(opts *gitlab.ListUsersOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.User, *gitlab.Response, error) {
		assert.Equal(t, "bob", *opts.Username)
		return []*gitlab.User{{ID: 3, Username: "bob"}}, nil, nil
	})
	tc.MockMergeRequests.EXPECT().ListGroupMergeRequests("team", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, opts *gitlab.ListGroupMergeRequestsOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "opened", *opts.State)
			assert.False(t, *opts.Draft)
			return nil, &gitlab.Response{}, nil
		})
	tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("team/app", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, &gitlab.Response{}, nil)
	tc.MockMergeRequests.EXPECT().UpdateMergeRequest("OWNER/REPO", int64(9), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, _ int64, opts *gitlab.UpdateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.ElementsMatch(t, []int64{2, 3}, []int64{(*opts.AssigneeIDs)[0], (*opts.ReviewerIDs)[0]})
			return &gitlab.MergeRequest{}, nil, nil
		})
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	_, err := exec("9 --group team --project team/app --users alice,alice,bob")
	require.NoError(t, err)
}

func TestAssignExcludesDiscoveredUser(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	author := &gitlab.BasicUser{ID: 1, Username: "author"}
	alice := &gitlab.BasicUser{ID: 2, Username: "alice"}
	bob := &gitlab.BasicUser{ID: 3, Username: "bob"}
	carol := &gitlab.BasicUser{ID: 4, Username: "carol"}
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 99, IID: 9, Author: author, Assignees: []*gitlab.BasicUser{bob}}}, nil, nil)
	tc.MockUsers.EXPECT().ListUsers(gomock.Any(), gomock.Any()).DoAndReturn(func(opts *gitlab.ListUsersOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.User, *gitlab.Response, error) {
		assert.Equal(t, "alice", *opts.Username)
		return []*gitlab.User{{ID: 2, Username: "alice"}}, nil, nil
	})
	tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).Return(
		[]*gitlab.BasicMergeRequest{
			{ID: 10, State: "opened", Author: alice, Assignees: []*gitlab.BasicUser{alice}},
			{ID: 11, State: "opened", Author: author, Assignees: []*gitlab.BasicUser{carol}},
		}, &gitlab.Response{}, nil)
	tc.MockMergeRequests.EXPECT().UpdateMergeRequest("OWNER/REPO", int64(9), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, _ int64, opts *gitlab.UpdateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			require.Nil(t, opts.AssigneeIDs)
			require.Equal(t, []int64{4}, *opts.ReviewerIDs)
			return &gitlab.MergeRequest{}, nil, nil
		})
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	_, err := exec("9 --users -alice")
	require.NoError(t, err)
}

func TestUserIDsIgnoresUnknownExclusion(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockUsers.EXPECT().ListUsers(gomock.Any(), gomock.Any()).DoAndReturn(func(opts *gitlab.ListUsersOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.User, *gitlab.Response, error) {
		assert.Equal(t, "missing", *opts.Username)
		return nil, nil, nil
	})
	ids, excluded, _, err := userIDs(context.Background(), tc.Client, []string{"-missing"})
	require.NoError(t, err)
	assert.Empty(t, ids)
	assert.Empty(t, excluded)
}

func TestUserIDsReturnsExcludedLookupError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockUsers.EXPECT().ListUsers(gomock.Any(), gomock.Any()).Return(nil, nil, errors.New("lookup failed"))
	_, _, _, err := userIDs(context.Background(), tc.Client, []string{"-alice"})
	require.ErrorContains(t, err, "lookup failed")
}

func TestUserIDsRejectsBareExclusion(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	_, _, _, err := userIDs(context.Background(), tc.Client, []string{"-"})
	require.ErrorContains(t, err, "requires a username")
}

func TestAssignDryRunReplacesExcludedCurrentAssignee(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	author := &gitlab.BasicUser{ID: 1, Username: "author"}
	alice := &gitlab.BasicUser{ID: 2, Username: "alice"}
	bob := &gitlab.BasicUser{ID: 3, Username: "bob"}
	carol := &gitlab.BasicUser{ID: 4, Username: "carol"}
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
			ID: 99, IID: 9, Author: author, Assignees: []*gitlab.BasicUser{alice}, Reviewers: []*gitlab.BasicUser{bob},
		}}, nil, nil)
	tc.MockUsers.EXPECT().ListUsers(gomock.Any(), gomock.Any()).DoAndReturn(func(opts *gitlab.ListUsersOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.User, *gitlab.Response, error) {
		assert.Equal(t, "alice", *opts.Username)
		return []*gitlab.User{{ID: 2, Username: "alice"}}, nil, nil
	})
	tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).Return(
		[]*gitlab.BasicMergeRequest{{ID: 10, State: "opened", Author: bob, Assignees: []*gitlab.BasicUser{carol}}}, &gitlab.Response{}, nil)
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	out, err := exec("9 --users=-alice --dry-run")
	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), "Assignees: @alice -> @carol")
	assert.Contains(t, out.OutBuf.String(), "Reviewers: @bob -> @bob")
}

func TestAssignExclusionOverridesExplicitCandidate(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
			ID: 99, IID: 9, Author: &gitlab.BasicUser{ID: 1}, Reviewers: []*gitlab.BasicUser{{ID: 4}},
		}}, nil, nil)
	gomock.InOrder(
		tc.MockUsers.EXPECT().ListUsers(gomock.Any(), gomock.Any()).DoAndReturn(func(opts *gitlab.ListUsersOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.User, *gitlab.Response, error) {
			assert.Equal(t, "alice", *opts.Username)
			return []*gitlab.User{{ID: 2, Username: "alice"}}, nil, nil
		}),
		tc.MockUsers.EXPECT().ListUsers(gomock.Any(), gomock.Any()).DoAndReturn(func(opts *gitlab.ListUsersOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.User, *gitlab.Response, error) {
			assert.Equal(t, "bob", *opts.Username)
			return []*gitlab.User{{ID: 3, Username: "bob"}}, nil, nil
		}),
	)
	tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, &gitlab.Response{}, nil)
	tc.MockMergeRequests.EXPECT().UpdateMergeRequest("OWNER/REPO", int64(9), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, _ int64, opts *gitlab.UpdateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			require.Equal(t, []int64{3}, *opts.AssigneeIDs)
			require.Nil(t, opts.ReviewerIDs)
			return &gitlab.MergeRequest{}, nil, nil
		})
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	_, err := exec("9 --users alice,-alice,bob")
	require.NoError(t, err)
}

func TestAssignmentOptionsUsesEitherDiscoveredRoleForMissingRole(t *testing.T) {
	t.Parallel()

	author := &gitlab.BasicUser{ID: 1}
	user := &gitlab.BasicUser{ID: 2}
	mrs := []*gitlab.BasicMergeRequest{{ID: 10, State: "opened", Author: author, Reviewers: []*gitlab.BasicUser{user}}}
	needsAssignee := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
		ID: 99, Author: author, Reviewers: []*gitlab.BasicUser{{ID: 3}},
	}}
	opts, err := assignmentOptions(needsAssignee, mrs, nil, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, *opts.AssigneeIDs)
	require.Nil(t, opts.ReviewerIDs)

	mrs[0].Assignees = []*gitlab.BasicUser{user}
	mrs[0].Reviewers = nil
	needsReviewer := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
		ID: 99, Author: author, Assignees: []*gitlab.BasicUser{{ID: 3}},
	}}
	opts, err = assignmentOptions(needsReviewer, mrs, nil, nil)
	require.NoError(t, err)
	require.Nil(t, opts.AssigneeIDs)
	require.Equal(t, []int64{2}, *opts.ReviewerIDs)
}

func TestAssignFailsWithoutEligibleCandidates(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 99, IID: 9, Author: &gitlab.BasicUser{ID: 1}}}, nil, nil)
	tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, &gitlab.Response{}, nil)
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	_, err := exec("9")
	require.ErrorContains(t, err, "no eligible")
}

func TestAssignSkipsScanWhenRolesAreFilled(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
			ID: 99, IID: 9, Author: &gitlab.BasicUser{ID: 1},
			Assignees: []*gitlab.BasicUser{{ID: 2}}, Reviewers: []*gitlab.BasicUser{{ID: 3}},
		}}, nil, nil)
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	out, err := exec("9 --group team")
	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), "Author: ID 1\nMerge request !9 already has an assignee and reviewer.")
}

func TestAssignShowsUnknownAuthor(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
			ID: 99, IID: 9, Assignees: []*gitlab.BasicUser{{ID: 2}}, Reviewers: []*gitlab.BasicUser{{ID: 3}},
		}}, nil, nil)
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	out, err := exec("9")
	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), "Author: (unknown)\nMerge request !9 already has an assignee and reviewer.")
}

func TestAssignScansAllPages(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	author := &gitlab.BasicUser{ID: 1}
	alice := &gitlab.BasicUser{ID: 2}
	bob := &gitlab.BasicUser{ID: 3}
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 99, IID: 9, Author: author}}, nil, nil)
	gomock.InOrder(
		tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).Return(
			[]*gitlab.BasicMergeRequest{{ID: 10, State: "opened", Author: author, Assignees: []*gitlab.BasicUser{alice}}}, &gitlab.Response{NextPage: 2}, nil),
		tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).Return(
			[]*gitlab.BasicMergeRequest{{ID: 11, State: "opened", Author: author, Reviewers: []*gitlab.BasicUser{bob}}}, &gitlab.Response{}, nil),
	)
	tc.MockMergeRequests.EXPECT().UpdateMergeRequest("OWNER/REPO", int64(9), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, _ int64, opts *gitlab.UpdateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.ElementsMatch(t, []int64{2, 3}, []int64{(*opts.AssigneeIDs)[0], (*opts.ReviewerIDs)[0]})
			return &gitlab.MergeRequest{}, nil, nil
		})
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	_, err := exec("9")
	require.NoError(t, err)
}

func TestAssignDoesNotUpdateAfterScanFailure(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 99, IID: 9}}, nil, nil)
	tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).Return(
		nil, nil, errors.New("scan failed"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	_, err := exec("9")
	require.ErrorContains(t, err, "scan failed")
}

func TestAssignDryRunPreviewsReplacementWithoutUpdating(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	author := &gitlab.BasicUser{ID: 1, Username: "author"}
	alice := &gitlab.BasicUser{ID: 2, Username: "alice"}
	bob := &gitlab.BasicUser{ID: 3, Username: "bob"}
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
			ID: 99, IID: 9, Author: author, Assignees: []*gitlab.BasicUser{author}, Reviewers: []*gitlab.BasicUser{bob},
		}}, nil, nil)
	tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).Return(
		[]*gitlab.BasicMergeRequest{{ID: 10, State: "opened", Author: bob, Assignees: []*gitlab.BasicUser{alice}}}, &gitlab.Response{}, nil)
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	out, err := exec("9 --dry-run")
	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), "Dry run for merge request !9:\nAuthor: @author\nAssignees:")
	assert.Contains(t, out.OutBuf.String(), "Assignees: @author -> @alice")
	assert.Contains(t, out.OutBuf.String(), "Reviewers: @bob -> @bob")
	assert.Contains(t, out.OutBuf.String(), "No changes were made.")
}

func TestAssignDryRunPreviewsBothNewRoles(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	author := &gitlab.BasicUser{ID: 1, Username: "author"}
	alice := &gitlab.BasicUser{ID: 2, Username: "alice"}
	bob := &gitlab.BasicUser{ID: 3, Username: "bob"}
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 99, IID: 9, Author: author}}, nil, nil)
	tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).Return(
		[]*gitlab.BasicMergeRequest{{ID: 10, State: "opened", Author: author, Assignees: []*gitlab.BasicUser{alice}, Reviewers: []*gitlab.BasicUser{bob}}}, &gitlab.Response{}, nil)
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	out, err := exec("9 --dry-run")
	require.NoError(t, err)
	preview := out.OutBuf.String()
	if strings.Contains(preview, "Assignees: (none) -> @alice") {
		assert.Contains(t, preview, "Reviewers: (none) -> @bob")
	} else {
		assert.Contains(t, preview, "Assignees: (none) -> @bob")
		assert.Contains(t, preview, "Reviewers: (none) -> @alice")
	}
}

func TestAssignDryRunShowsUnchangedAssignments(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
			ID: 99, IID: 9, Author: &gitlab.BasicUser{ID: 1},
			Assignees: []*gitlab.BasicUser{{ID: 2}}, Reviewers: []*gitlab.BasicUser{{ID: 3}},
		}}, nil, nil)
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	out, err := exec("9 --dry-run")
	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), "Assignees: ID 2 -> ID 2")
	assert.Contains(t, out.OutBuf.String(), "Reviewers: ID 3 -> ID 3")
}

func TestAssignDryRunReturnsSelectionError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(9), gomock.Any()).Return(
		&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 99, IID: 9, Author: &gitlab.BasicUser{ID: 1}}}, nil, nil)
	tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, &gitlab.Response{}, nil)
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	out, err := exec("9 --dry-run")
	require.ErrorContains(t, err, "no eligible")
	assert.NotContains(t, out.OutBuf.String(), "Author:")
}
