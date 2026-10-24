package pluginhost

import (
	"context"
	"fmt"

	cordis "github.com/tr1v3r/cordis-go"
)

// One Cordis root owns one immutable runtime generation. Host.lifecycleMu
// serializes transitions: Cordis Dispose may otherwise defer teardown while a
// fiber is loading. Returning from Wuu retirement is a cleanup barrier.
//
// Each transport is owned outside its dependency-injected activation fiber so
// it survives until the consumer's shutdown calls have finished, including for
// prepared transports whose activation never ran.
type clientLifetime struct {
	owner       *cordis.Fiber
	preparation *cordis.Fiber
	activation  *cordis.Fiber
	closeCtx    context.Context
	closeErr    error
}

func (h *Host) ownClients(clients []Client) error {
	if h.lifecycleRoot == nil {
		h.lifecycleRoot = cordis.New()
		h.lifetimes = make(map[string]*clientLifetime)
	}
	for _, client := range clients {
		if h.lifetimes[client.ID()] != nil {
			continue
		}
		lifetime := &clientLifetime{}
		owner := cordis.Define[struct{}](client.ID(), func(scope *cordis.Context, _ struct{}) error {
			scope.OnDispose(func() {
				h.serviceRegistry.cancelCallerScopes(client.ID())
				finishCleanup := h.serviceRegistry.allowShutdownCleanup(client.ID())
				defer finishCleanup()
				defer func() {
					if value := recover(); value != nil {
						lifetime.closeErr = fmt.Errorf("close plugin %q panicked: %v", client.ID(), value)
					}
				}()
				lifetime.closeErr = client.Close(lifetime.closeCtx)
			})
			return nil
		})
		fiber, err := h.lifecycleRoot.Load(owner, struct{}{})
		if err != nil {
			return fmt.Errorf("own plugin %q: %w", client.ID(), err)
		}
		lifetime.owner = fiber
		h.lifetimes[client.ID()] = lifetime
	}
	return nil
}

func (h *Host) prepareClient(ctx context.Context, client Client, dependencies []Dependency) error {
	lifetime := h.lifetimes[client.ID()]
	if lifetime.preparation != nil {
		return lifetime.preparation.Error()
	}
	var inject []string
	for _, dependency := range dependencies {
		if dependency.Required && h.lifetimes[dependency.Provider] != nil {
			inject = append(inject, "prepared:"+dependency.Provider)
		}
	}
	preparation := cordis.Define[struct{}](client.ID()+":preparation", func(scope *cordis.Context, _ struct{}) error {
		finish := h.serviceRegistry.allowPreparation(client.ID())
		defer finish()
		if lifecycle, ok := client.(preparationClient); ok {
			if err := lifecycle.Prepare(ctx); err != nil {
				return err
			}
		}
		_, err := scope.Provide("prepared:"+client.ID(), client)
		return err
	}).WithInject(inject...)
	fiber, err := lifetime.owner.Ctx.Load(preparation, struct{}{})
	lifetime.preparation = fiber
	if err != nil {
		return err
	}
	if fiber.State() != cordis.StateActive {
		return fmt.Errorf("plugin %q preparation is %s: required provider is unavailable", client.ID(), fiber.State())
	}
	return nil
}

func (h *Host) activateClient(ctx context.Context, client Client, dependencies []Dependency) error {
	lifetime := h.lifetimes[client.ID()]
	if lifetime.activation != nil {
		return lifetime.activation.Error()
	}
	var inject []string
	for _, dependency := range dependencies {
		// Declarative packages have no transport. Their availability has already
		// been checked during composition, including transitive failures.
		if dependency.Required && h.lifetimes[dependency.Provider] != nil {
			inject = append(inject, "plugin:"+dependency.Provider)
		}
	}
	activation := cordis.Define[struct{}](client.ID()+":activation", func(scope *cordis.Context, _ struct{}) error {
		if lifecycle, ok := client.(lifecycleClient); ok && client.Status().State == StatePrepared {
			if err := lifecycle.Activate(ctx); err != nil {
				return err
			}
		}
		_, err := scope.Provide("plugin:"+client.ID(), client)
		return err
	}).WithInject(inject...)
	fiber, err := lifetime.owner.Ctx.Load(activation, struct{}{})
	lifetime.activation = fiber
	if err != nil {
		return err
	}
	if fiber.State() != cordis.StateActive {
		return fmt.Errorf("plugin %q activation is %s: required provider is unavailable", client.ID(), fiber.State())
	}
	return nil
}

func (h *Host) closeClient(ctx context.Context, client Client) error {
	lifetime := h.lifetimes[client.ID()]
	// Close is a completion barrier even when the initiating waiter expired.
	lifetime.closeCtx = context.WithValue(context.WithoutCancel(ctx), serviceCleanupKey{}, true)
	lifetime.owner.Dispose()
	delete(h.lifetimes, client.ID())
	return lifetime.closeErr
}
