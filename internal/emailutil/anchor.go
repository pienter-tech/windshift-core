package emailutil

import "fmt"

// AnchorMessageID is the synthetic RFC 5322 Message-ID minted as the thread
// root for portal/form-originated tickets (WI-1546): outbound notifications
// chain In-Reply-To/References from it, and email replies quoting it thread
// back onto the portal ticket. Shared by the anchor minter and tests so the
// format has exactly one definition.
func AnchorMessageID(itemID int64, domain string) string {
	return fmt.Sprintf("<ws-item-%d@%s>", itemID, domain)
}
