/*

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

package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/dsyer/spring-service-operator/api/v1"
)

// ProxyServiceReconciler reconciles a ProxyService object
type ProxyServiceReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=spring.io,resources=proxyservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=spring.io,resources=proxyservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;update;patch;delete

var (
	ownerKey = ".metadata.controller"
	apiGVStr = api.GroupVersion.String()
)

// Reconcile Business logic for controller
func (r *ProxyServiceReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("ProxyService", req.NamespacedName)

	var micro api.ProxyService
	if err := r.Get(ctx, req.NamespacedName, &micro); err != nil {
		err = client.IgnoreNotFound(err)
		if err != nil {
			log.Error(err, "Unable to fetch micro")
		}
		return ctrl.Result{}, err
	}
	log.Info("Updating", "resource", micro)

	if err := r.Status().Update(ctx, &micro); err != nil {
		if apierrors.IsConflict(err) {
			log.Info("Unable to update status: reason conflict. Will retry on next event.")
			err = nil
		} else {
			log.Error(err, "Unable to update micro status")
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ProxyServiceReconciler) createAndUpdateDeploymentAndService(req ctrl.Request, micro api.ProxyService) (*corev1.Service, error) {

	ctx := context.Background()
	log := r.Log.WithValues("ProxyService", req.NamespacedName)

	var services corev1.ServiceList
	var service *corev1.Service

	if err := r.List(ctx, &services, client.InNamespace(req.Namespace), client.MatchingFields{ownerKey: req.Name}); err != nil {
		log.Error(err, "Unable to list child Services")
		return service, err
	}

	log.Info("Found services", "services", len(services.Items))
	if len(services.Items) == 0 {
		var err error
		service, err = r.constructService(&micro)
		if err != nil {
			return service, err
		}
		if err := r.Create(ctx, service); err != nil {
			log.Error(err, "Unable to create Service for micro", "service", service)
			return service, err
		}

		log.Info("Created Service for micro", "service", service)
		r.Recorder.Event(&micro, corev1.EventTypeNormal, "ServiceCreated", "Created Service")
	} else {
		// update if changed
		service = &services.Items[0]
		service = updateService(service, &micro)
		if err := r.Update(ctx, service); err != nil {
			if apierrors.IsConflict(err) {
				log.Info("Unable to update Service: reason conflict. Will retry on next event.")
				err = nil
			} else {
				log.Error(err, "Unable to update Service for micro", "service", service)
				r.Recorder.Event(&micro, corev1.EventTypeWarning, "ErrInvalidResource", fmt.Sprintf("Could not update Service: %s", err))
			}
			return service, err
		}

		log.Info("Updated Service for micro", "service", service)
		r.Recorder.Event(&micro, corev1.EventTypeNormal, "ServiceUpdated", "Updated Service")
	}
	return service, nil
}

func createService(micro *api.ProxyService) *corev1.Service {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Labels:    map[string]string{"app": micro.Name},
			Name:      micro.Name,
			Namespace: micro.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				corev1.ServicePort{
					Protocol:   "TCP",
					Port:       80,
					TargetPort: intstr.FromInt(8080),
					Name:       "http",
				},
			},
			Selector: map[string]string{"app": micro.Name},
		},
	}
	return service
}

func updateService(service *corev1.Service, micro *api.ProxyService) *corev1.Service {
	return service
}

func (r *ProxyServiceReconciler) constructService(micro *api.ProxyService) (*corev1.Service, error) {
	service := createService(micro)
	if err := ctrl.SetControllerReference(micro, service, r.Scheme); err != nil {
		return nil, err
	}
	return service, nil
}

// SetupWithManager Utility method to set up manager
func (r *ProxyServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(&corev1.Service{}, ownerKey, func(rawObj runtime.Object) []string {
		// grab the service object, extract the owner...
		service := rawObj.(*corev1.Service)
		owner := metav1.GetControllerOf(service)
		if owner == nil {
			return nil
		}
		// ...make sure it's ours...
		if owner.APIVersion != apiGVStr || owner.Kind != "ProxyService" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.ProxyService{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
