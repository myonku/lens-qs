package server

import "testing"

func TestG2SharedNullAndLogoutContracts(t *testing.T) {
	f := newAPI(t)
	for _, field := range []string{"display_name", "is_admin"} {
		f.call("POST", "/users", `{"username":"bad","password":"good-password","`+field+`":null}`, 400)
	}
	f.call("POST", "/users", `{"username":"nullable","password":"good-password","employee_no":null,"department":null}`, 201)
	for _, field := range []string{"label", "sort_no", "active"} {
		f.call("POST", "/dict/items", `{"category":"sample_model","code":"NULL","`+field+`":null}`, 400)
	}
	for _, path := range []string{"/users?page=", "/users?page_size=", "/dict/drawings?page_size="} {
		f.call("GET", path, "", 400)
	}
	for _, field := range []string{"lines", "quantity_changes"} {
		f.call("POST", "/submissions", `{"content":{"`+field+`":[null]}}`, 400)
	}
	f.call("POST", "/dict/drawings/import", `{"items":[{"drawing_no":"null-active","active":null}]}`, 400)
	f.call("POST", "/auth/logout", `{}`, 400)
	f.call("GET", "/auth/me", "", 200)
	f.call("POST", "/auth/logout", "", 200)
	f.call("GET", "/auth/me", "", 401)
}
