package v2

import (
	"net/http"

	"windshift/internal/repository"
)

// registerItemSCMLinkRoutes exposes an item's source-control links (pull
// requests, branches, commits) read-only, so API tokens can find an item's PR.
func registerItemSCMLinkRoutes(builder *routeBuilder, deps Deps) {
	builder.Read("/items/{item_id}/scm-links", AuthAuthenticated, []string{"items:read"}, listItemSCMLinks(deps))
}

func listItemSCMLinks(deps Deps) readOperation[[]repository.ItemSCMLink] {
	return func(r *http.Request) ([]repository.ItemSCMLink, error) {
		item, err := requireItem(r, deps, deps.Access.CanViewWorkspace)
		if err != nil {
			return nil, err
		}
		links, err := deps.SCMLinks.ListItemSCMLinks(item.ID)
		if err != nil {
			return nil, internalError(err)
		}
		return links, nil
	}
}
