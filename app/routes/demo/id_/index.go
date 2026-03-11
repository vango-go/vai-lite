package id_

import (
	"github.com/vango-go/vai-lite/app/components"
	"github.com/vango-go/vango"
	. "github.com/vango-go/vango/el"
)

type Params struct {
	ID string `param:"id"`
}

func ShowPage(ctx vango.Ctx, p Params) *vango.VNode {
	actor, ok := components.CurrentActor(ctx)
	if !ok {
		return components.AuthRedirectPage()
	}
	return Fragment(components.ChatPage(components.ChatPageProps{
		Actor:          actor,
		ConversationID: p.ID,
	}))
}
