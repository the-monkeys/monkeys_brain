package consumer

import (
	"context"
	"testing"

	"github.com/the-monkeys/the_monkeys/constants"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/database"
	"github.com/the-monkeys/the_monkeys/microservices/the_monkeys_blog/internal/models"
	"go.uber.org/zap"
)

type fakeES struct {
	database.ElasticsearchStorage
	gotSlug string
	called  bool
}

func (f *fakeES) DetachBlogsFromGroup(_ context.Context, groupSlug string) error {
	f.called = true
	f.gotSlug = groupSlug
	return nil
}

func TestHandleGroupDeleteDetachesBlogs(t *testing.T) {
	es := &fakeES{}
	handleUserAction(models.InterServiceMessage{
		Action:    constants.GROUP_DELETE,
		GroupSlug: "tea-club",
	}, zap.NewNop().Sugar(), es)
	if es.gotSlug != "tea-club" {
		t.Fatalf("detached slug %q", es.gotSlug)
	}
}

func TestHandleGroupDeleteEmptySlugDoesNothing(t *testing.T) {
	es := &fakeES{}
	handleUserAction(models.InterServiceMessage{
		Action: constants.GROUP_DELETE,
	}, zap.NewNop().Sugar(), es)
	if es.called {
		t.Fatal("empty slug should not detach")
	}
}
