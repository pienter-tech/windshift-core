// A request is external when it was submitted by a portal customer (portal,
// public form, or helpdesk email intake) or through a request type (portal or
// form submissions by an internal user). Items created directly by agents have
// neither and stay internal.
//
// The request-type check alone misses helpdesk email: the email processor sets
// the channel and creator customer but no request type. `creator_portal_customer_id`
// covers that origin.
export function isExternalRequest(item) {
  return Boolean(item?.request_type_id || item?.creator_portal_customer_id);
}
