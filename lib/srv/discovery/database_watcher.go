/*
Copyright 2022 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package discovery

import (
	"context"
	"sync"

	"github.com/gravitational/trace"

	usageeventsv1 "github.com/gravitational/teleport/api/gen/proto/go/usageevents/v1"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/srv/discovery/common"
)

const databaseEventPrefix = "db/"

func (s *Server) startDatabaseWatchers() error {
	if len(s.databaseFetchers) == 0 && s.dynamicMatcherWatcher == nil {
		return nil
	}

	var (
		newDatabases types.ResourcesWithLabels
		mu           sync.Mutex
	)

	reconciler, err := services.NewReconciler(
		services.ReconcilerConfig{
			Matcher:             func(_ types.ResourceWithLabels) bool { return true },
			GetCurrentResources: s.getCurrentDatabases,
			GetNewResources: func() types.ResourcesWithLabelsMap {
				mu.Lock()
				defer mu.Unlock()
				return newDatabases.ToMap()
			},
			Log:      s.Log.WithField("kind", types.KindDatabase),
			OnCreate: s.onDatabaseCreate,
			OnUpdate: s.onDatabaseUpdate,
			OnDelete: s.onDatabaseDelete,
		},
	)
	if err != nil {
		return trace.Wrap(err)
	}

	watcher, err := common.NewWatcher(s.ctx, common.WatcherConfig{
		FetchersFn:     s.getAllDatabaseFetchers,
		Log:            s.Log.WithField("kind", types.KindDatabase),
		DiscoveryGroup: s.DiscoveryGroup,
		Interval:       s.PollInterval,
		Origin:         types.OriginCloud,
		Clock:          s.clock,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	go watcher.Start()

	go func() {
		for {
			select {
			case newResources := <-watcher.ResourcesC():
				mu.Lock()
				newDatabases = newResources
				mu.Unlock()

				if err := reconciler.Reconcile(s.ctx); err != nil {
					s.Log.WithError(err).Warn("Unable to reconcile database resources.")
				} else if s.onDatabaseReconcile != nil {
					s.onDatabaseReconcile()
				}

			case <-s.ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (s *Server) getAllDatabaseFetchers() []common.Fetcher {
	allFetchers := make([]common.Fetcher, 0, len(s.databaseFetchers))

	s.muDynamicFetchers.RLock()
	for _, fetcherSet := range s.dynamicDatabaseFetchers {
		allFetchers = append(allFetchers, fetcherSet...)
	}
	s.muDynamicFetchers.RUnlock()

	return append(allFetchers, s.databaseFetchers...)
}

func (s *Server) getCurrentDatabases() types.ResourcesWithLabelsMap {
	databases, err := s.AccessPoint.GetDatabases(s.ctx)
	if err != nil {
		s.Log.WithError(err).Warn("Failed to get databases from cache.")
		return nil
	}

	return types.Databases(filterResources(databases, types.OriginCloud, s.DiscoveryGroup)).AsResources().ToMap()
}

func (s *Server) onDatabaseCreate(ctx context.Context, resource types.ResourceWithLabels) error {
	database, ok := resource.(types.Database)
	if !ok {
		return trace.BadParameter("invalid type received; expected types.Database, received %T", database)
	}
	s.Log.Debugf("Creating database %s.", database.GetName())
	err := s.AccessPoint.CreateDatabase(ctx, database)
	// If the resource already exists, it means that the resource was created
	// by a previous discovery_service instance that didn't support the discovery
	// group feature or the discovery group was changed.
	// In this case, we need to update the resource with the
	// discovery group label to ensure the user doesn't have to manually delete
	// the resource.
	// TODO(tigrato): DELETE on 15.0.0
	if trace.IsAlreadyExists(err) {
		return trace.Wrap(s.onDatabaseUpdate(ctx, resource))
	}
	if err != nil {
		return trace.Wrap(err)
	}
	err = s.emitUsageEvents(map[string]*usageeventsv1.ResourceCreateEvent{
		databaseEventPrefix + database.GetName(): {
			ResourceType:   types.DiscoveredResourceDatabase,
			ResourceOrigin: types.OriginCloud,
			CloudProvider:  database.GetCloud(),
			Database: &usageeventsv1.DiscoveredDatabaseMetadata{
				DbType:     database.GetType(),
				DbProtocol: database.GetProtocol(),
			},
		},
	})
	if err != nil {
		s.Log.WithError(err).Debug("Error emitting usage event.")
	}
	return nil
}

func (s *Server) onDatabaseUpdate(ctx context.Context, resource types.ResourceWithLabels) error {
	database, ok := resource.(types.Database)
	if !ok {
		return trace.BadParameter("invalid type received; expected types.Database, received %T", database)
	}
	s.Log.Debugf("Updating database %s.", database.GetName())
	return trace.Wrap(s.AccessPoint.UpdateDatabase(ctx, database))
}

func (s *Server) onDatabaseDelete(ctx context.Context, resource types.ResourceWithLabels) error {
	database, ok := resource.(types.Database)
	if !ok {
		return trace.BadParameter("invalid type received; expected types.Database, received %T", database)
	}
	s.Log.Debugf("Deleting database %s.", database.GetName())
	return trace.Wrap(s.AccessPoint.DeleteDatabase(ctx, database.GetName()))
}

func filterResources[T types.ResourceWithLabels, S ~[]T](all S, wantOrigin, wantResourceGroup string) (filtered S) {
	for _, resource := range all {
		resourceDiscoveryGroup, _ := resource.GetLabel(types.TeleportInternalDiscoveryGroupName)
		if (wantOrigin != "" && resource.Origin() != wantOrigin) || resourceDiscoveryGroup != wantResourceGroup {
			continue
		}
		filtered = append(filtered, resource)

	}
	return
}
