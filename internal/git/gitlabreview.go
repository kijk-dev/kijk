package git

import "errors"

func gitlabComment(f *forge, n int, head string, c NewComment) (string, error) {
	return "", errors.New("not on GitLab yet")
}

func gitlabEdit(f *forge, n int, id, body string) error { return errors.New("not on GitLab yet") }

func gitlabDelete(f *forge, n int, id string) error { return errors.New("not on GitLab yet") }

func gitlabResolve(f *forge, n int, thread string, resolved bool) error {
	return errors.New("not on GitLab yet")
}

func gitlabSubmit(f *forge, n int, verdict, body string) error {
	return errors.New("not on GitLab yet")
}
