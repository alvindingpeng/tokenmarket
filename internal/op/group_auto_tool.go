package op

// GroupsContainingGrant returns group IDs whose existing member set contains the
// grant. It deliberately uses the cached raw group items, not a viewer-filtered
// snapshot, so a verification can update only groups that already reference the
// grant.
func GroupsContainingGrant(grantID int) []int {
	groups := groupCache.GetAll()
	ids := make([]int, 0)
	for _, group := range groups {
		for _, item := range group.Items {
			if item.ChannelGrantID == grantID {
				ids = append(ids, group.ID)
				break
			}
		}
	}
	return ids
}
