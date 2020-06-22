package foo

import (
	"context"

	v1 "github.com/mrajashree/backup/pkg/apis/some.api.group/v1"
	foocontroller "github.com/mrajashree/backup/pkg/generated/controllers/some.api.group/v1"
)

type Controller struct {
	foos foocontroller.FooController
}

func Register(
	ctx context.Context,
	foos foocontroller.FooController) {

	controller := &Controller{
		foos: foos,
	}

	foos.OnChange(ctx, "foo-handler", controller.OnFooChange)
	foos.OnRemove(ctx, "foo-handler", controller.OnFooRemove)
}

func (c *Controller) OnFooChange(key string, foo *v1.Foo) (*v1.Foo, error) {
	//change logic, return original foo if no changes

	fooCopy := foo.DeepCopy()
	//make changes to fooCopy
	return c.foos.Update(fooCopy)
}

func (c *Controller) OnFooRemove(key string, foo *v1.Foo) (*v1.Foo, error) {
	//remove logic, return original foo if no changes

	fooCopy := foo.DeepCopy()
	//make changes to fooCopy
	return c.foos.Update(fooCopy)
}
