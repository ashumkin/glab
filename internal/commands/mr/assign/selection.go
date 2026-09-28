package assign

import (
	"errors"
	"math/rand/v2"
	"slices"
	"sort"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

func assignmentOptions(target *gitlab.MergeRequest, mrs []*gitlab.BasicMergeRequest, users []int64, excluded map[int64]struct{}) (*gitlab.UpdateMergeRequestOptions, error) {
	candidates := make(map[int64]struct{})
	load := make(map[int64]int)
	for _, id := range users {
		candidates[id] = struct{}{}
	}

	seen := make(map[int64]struct{})
	for _, mr := range mrs {
		if mr == nil || mr.ID == target.ID || mr.State != "opened" || mr.Draft {
			continue
		}
		if _, ok := seen[mr.ID]; ok {
			continue
		}
		seen[mr.ID] = struct{}{}

		members := make(map[int64]struct{})
		selfAssigned := false
		for _, user := range mr.Assignees {
			if user == nil {
				continue
			}
			candidates[user.ID] = struct{}{}
			members[user.ID] = struct{}{}
			selfAssigned = selfAssigned || mr.Author != nil && user.ID == mr.Author.ID
		}
		if mr.Assignee != nil && len(mr.Assignees) == 0 {
			candidates[mr.Assignee.ID] = struct{}{}
			members[mr.Assignee.ID] = struct{}{}
			selfAssigned = mr.Author != nil && mr.Assignee.ID == mr.Author.ID
		}
		for _, user := range mr.Reviewers {
			if user == nil {
				continue
			}
			candidates[user.ID] = struct{}{}
			members[user.ID] = struct{}{}
		}
		if !selfAssigned {
			for id := range members {
				load[id]++
			}
		}
	}
	for id := range excluded {
		delete(candidates, id)
	}

	needsAssignee, needsReviewer, assigned, reviewers := rolesNeeded(target, excluded)
	opts := &gitlab.UpdateMergeRequestOptions{}
	if needsAssignee == 0 && needsReviewer == 0 {
		return opts, nil
	}
	var authorID int64
	if target.Author != nil {
		authorID = target.Author.ID
	}
	choices := make([]int64, 0, len(candidates))
	for id := range candidates {
		if id != 0 && id != authorID && !slices.Contains(assigned, id) && !slices.Contains(reviewers, id) {
			choices = append(choices, id)
		}
	}
	if len(choices) < needsAssignee+needsReviewer {
		return nil, errors.New("no eligible assignee and reviewer combination")
	}
	rand.Shuffle(len(choices), func(i, j int) { choices[i], choices[j] = choices[j], choices[i] })
	sort.SliceStable(choices, func(i, j int) bool { return load[choices[i]] < load[choices[j]] })
	if needsAssignee > 0 {
		ids := append(assigned, choices[:needsAssignee]...)
		opts.AssigneeIDs = &ids
	}
	if needsReviewer > 0 {
		ids := append(reviewers, choices[needsAssignee:needsAssignee+needsReviewer]...)
		opts.ReviewerIDs = &ids
	}
	return opts, nil
}

// rolesNeeded counts replacements for empty roles, excluded users, and the
// author as assignee while retaining other assignments in their current order.
func rolesNeeded(target *gitlab.MergeRequest, excluded map[int64]struct{}) (int, int, []int64, []int64) {
	var authorID int64
	if target.Author != nil {
		authorID = target.Author.ID
	}
	assignees := target.Assignees
	if len(assignees) == 0 && target.Assignee != nil {
		assignees = []*gitlab.BasicUser{target.Assignee}
	}
	assigned := make([]int64, 0, len(assignees))
	needsAssignee := 0
	for _, user := range assignees {
		if user == nil || user.ID == authorID {
			needsAssignee++
		} else if _, ok := excluded[user.ID]; ok {
			needsAssignee++
		} else {
			assigned = append(assigned, user.ID)
		}
	}
	if target.Assignee != nil && len(target.Assignees) > 0 {
		_, isExcluded := excluded[target.Assignee.ID]
		if (target.Assignee.ID == authorID || isExcluded) && !slices.ContainsFunc(target.Assignees, func(user *gitlab.BasicUser) bool {
			return user != nil && user.ID == target.Assignee.ID
		}) {
			needsAssignee++
		}
	}
	if len(assignees) == 0 {
		needsAssignee = 1
	}
	reviewers := make([]int64, 0, len(target.Reviewers))
	needsReviewer := 0
	for _, user := range target.Reviewers {
		if user == nil {
			needsReviewer++
		} else if _, ok := excluded[user.ID]; ok {
			needsReviewer++
		} else {
			reviewers = append(reviewers, user.ID)
		}
	}
	if len(target.Reviewers) == 0 {
		needsReviewer = 1
	}
	return needsAssignee, needsReviewer, assigned, reviewers
}
