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
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"strings"
	"text/template"

	"github.com/go-logr/logr"
	apps "k8s.io/api/apps/v1"
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
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
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

	proxy := &api.ProxyService{}
	if err := r.Get(ctx, req.NamespacedName, proxy); err != nil {
		err = client.IgnoreNotFound(err)
		if err != nil {
			log.Error(err, "Unable to fetch micro")
		}
		return ctrl.Result{}, err
	}
	log.Info("Updating", "resource", proxy)

	if err := r.checkFinalizers(proxy); err != nil {
		return ctrl.Result{}, err
	}

	deployment, _, err := r.createAndUpdateDeploymentAndService(req, *proxy)
	if err != nil {
		return ctrl.Result{}, err
	}
	proxy.Status.Running = deployment.Status.AvailableReplicas > 0

	if err := r.Status().Update(ctx, proxy); err != nil {
		if apierrors.IsConflict(err) {
			log.Info("Unable to update status: reason conflict. Will retry on next event.")
			err = nil
		} else {
			log.Error(err, "Unable to update proxy status")
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ProxyServiceReconciler) checkFinalizers(proxy *api.ProxyService) error {

	finalizerName := "spring.io/proxyservice"

    // examine DeletionTimestamp to determine if object is under deletion
    if proxy.ObjectMeta.DeletionTimestamp.IsZero() {
        if !containsString(proxy.ObjectMeta.Finalizers, finalizerName) {
            proxy.ObjectMeta.Finalizers = append(proxy.ObjectMeta.Finalizers, finalizerName)
            if err := r.Update(context.Background(), proxy); err != nil {
                return err
            }
        }
    } else {
        // The object is being deleted
        if containsString(proxy.ObjectMeta.Finalizers, finalizerName) {
            // our finalizer is present, so lets handle any external dependency
            if err := r.deleteExternalResources(proxy); err != nil {
                return err
            }

            // remove our finalizer from the list and update it.
            proxy.ObjectMeta.Finalizers = removeString(proxy.ObjectMeta.Finalizers, finalizerName)
            if err := r.Update(context.Background(), proxy); err != nil {
                return err
            }
        }
	}
	
	return nil
}

func (r *ProxyServiceReconciler) deleteExternalResources(proxy *api.ProxyService) error {
	return nil
}

func containsString(slice []string, s string) bool {
    for _, item := range slice {
        if item == s {
            return true
        }
    }
    return false
}

func removeString(slice []string, s string) (result []string) {
    for _, item := range slice {
        if item == s {
            continue
        }
        result = append(result, item)
    }
    return
}

func (r *ProxyServiceReconciler) createAndUpdateDeploymentAndService(req ctrl.Request, proxy api.ProxyService) (*apps.Deployment, *corev1.Service, error) {

	ctx := context.Background()
	log := r.Log.WithValues("ProxyService", req.NamespacedName)

	var services corev1.ServiceList
	var configs corev1.ConfigMapList
	var deployments apps.DeploymentList
	var service *corev1.Service
	var config *corev1.ConfigMap
	var deployment *apps.Deployment

	if err := r.List(ctx, &services, client.InNamespace(req.Namespace), client.MatchingFields{ownerKey: req.Name}); err != nil {
		log.Error(err, "Unable to list child Services")
		return deployment, service, err
	}
	if err := r.List(ctx, &deployments, client.InNamespace(req.Namespace), client.MatchingFields{ownerKey: req.Name}); err != nil {
		log.Error(err, "Unable to list child Deployments")
		return deployment, service, err
	}
	if err := r.List(ctx, &configs, client.InNamespace(req.Namespace), client.MatchingFields{ownerKey: req.Name}); err != nil {
		log.Error(err, "Unable to list child ConfigMaps")
		return deployment, service, err
	}

	log.Info("Found configs", "configs", len(configs.Items))
	if len(configs.Items) == 0 {
		var err error
		config, err = r.constructConfig(&proxy)
		if err != nil {
			return deployment, service, err
		}
		if err := r.Create(ctx, config); err != nil {
			log.Error(err, "Unable to create Service for proxy", "config", config)
			return deployment, service, err
		}

		log.Info("Created Service for proxy", "config", config)
		r.Recorder.Event(&proxy, corev1.EventTypeNormal, "ConfigMapCreated", "Created ConfigMap")
	} else {
		// update if changed
		config = &configs.Items[0]
		config = updateConfig(config, &proxy)
		if err := r.Update(ctx, config); err != nil {
			if apierrors.IsConflict(err) {
				log.Info("Unable to update ConfigMap: reason conflict. Will retry on next event.")
				err = nil
			} else {
				log.Error(err, "Unable to update ConfigMap for proxy", "config", config)
				r.Recorder.Event(&proxy, corev1.EventTypeWarning, "ErrInvalidResource", fmt.Sprintf("Could not update Service: %s", err))
			}
			return deployment, service, err
		}

		log.Info("Updated ConfigMap for proxy", "config", config)
		r.Recorder.Event(&proxy, corev1.EventTypeNormal, "ConfigMapUpdated", "Updated ConfigMap")
	}

	log.Info("Found services", "services", len(services.Items))
	if len(services.Items) == 0 {
		var err error
		service, err = r.constructService(&proxy)
		if err != nil {
			return deployment, service, err
		}
		if err := r.Create(ctx, service); err != nil {
			log.Error(err, "Unable to create Service for proxy", "service", service)
			return deployment, service, err
		}

		log.Info("Created Service for proxy", "service", service)
		r.Recorder.Event(&proxy, corev1.EventTypeNormal, "ServiceCreated", "Created Service")
	} else {
		// update if changed
		service = &services.Items[0]
		service = updateService(service, &proxy)
		if err := r.Update(ctx, service); err != nil {
			if apierrors.IsConflict(err) {
				log.Info("Unable to update Service: reason conflict. Will retry on next event.")
				err = nil
			} else {
				log.Error(err, "Unable to update Service for proxy", "service", service)
				r.Recorder.Event(&proxy, corev1.EventTypeWarning, "ErrInvalidResource", fmt.Sprintf("Could not update Service: %s", err))
			}
			return deployment, service, err
		}

		log.Info("Updated Service for proxy", "service", service)
		r.Recorder.Event(&proxy, corev1.EventTypeNormal, "ServiceUpdated", "Updated Service")
	}

	if len(deployments.Items) == 0 {
		var err error
		deployment, err = r.constructDeployment(&proxy)
		if err != nil {
			return deployment, service, err
		}
		if err := r.Create(ctx, deployment); err != nil {
			log.Error(err, "Unable to create Deployment for proxy", "deployment", deployment)
			r.Recorder.Event(&proxy, corev1.EventTypeWarning, "ErrInvalidResource", fmt.Sprintf("Could not create Deployment: %s", err))
			return deployment, service, err
		}

		log.Info("Created Deployments for proxy", "deployment", deployment)
		r.Recorder.Event(&proxy, corev1.EventTypeNormal, "DeploymentCreated", "Created Deployment")
	} else {
		// update if changed
		deployment = &deployments.Items[0]
		deployment.Spec.Template = *updatePodTemplate(&deployment.Spec.Template, &proxy)
		if err := r.Update(ctx, deployment); err != nil {
			if apierrors.IsConflict(err) {
				log.Info("Unable to update Deployment: reason conflict. Will retry on next event.")
				err = nil
			} else {
				log.Error(err, "Unable to update Deployment for proxy", "deployment", deployment)
				r.Recorder.Event(&proxy, corev1.EventTypeWarning, "ErrInvalidResource", fmt.Sprintf("Could not update Deployment: %s", err))
			}
			return deployment, service, err
		}

		log.Info("Updated Deployments for proxy", "deployment", deployment)
		r.Recorder.Event(&proxy, corev1.EventTypeNormal, "DeploymentUpdated", "Updated Deployment")
	}

	return deployment, service, nil
}

// Create a ConfigMap for the proxyservice application
func (r *ProxyServiceReconciler) constructConfig(proxy *api.ProxyService) (*corev1.ConfigMap, error) {
	config := createConfig(*proxy)
	r.Log.Info("Config", "config", config)
	if err := ctrl.SetControllerReference(proxy, config, r.Scheme); err != nil {
		return nil, err
	}
	return config, nil
}

// Create a Deployment for the proxyservice application
func (r *ProxyServiceReconciler) constructDeployment(proxy *api.ProxyService) (*apps.Deployment, error) {
	deployment := createDeployment(*proxy)
	r.Log.Info("Deploying", "deployment", deployment)
	if err := ctrl.SetControllerReference(proxy, deployment, r.Scheme); err != nil {
		return nil, err
	}
	return deployment, nil
}

func createDeployment(proxy api.ProxyService) *apps.Deployment {
	deployment := &apps.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Labels:    map[string]string{"proxy": proxy.Name},
			Name:      proxy.Name,
			Namespace: proxy.Namespace,
		},
		Spec: apps.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"proxy": proxy.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"proxy": proxy.Name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						corev1.Container{
							Name:  "nginx",
							Image: "nginx",
							VolumeMounts: []corev1.VolumeMount{
								corev1.VolumeMount{
									Name:      "nginx",
									MountPath: "/etc/nginx",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						corev1.Volume{
							Name: "nginx",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: fmt.Sprintf("%s-config", proxy.Name)},
								},
							},
						},
					},
				},
			},
		},
	}
	return deployment
}

func updatePodTemplate(template *corev1.PodTemplateSpec, proxy *api.ProxyService) *corev1.PodTemplateSpec {
	// TODO: reconcile services in proxy
	return template
}

func createConfig(proxy api.ProxyService) *corev1.ConfigMap {
	data := readMap("etc", proxy)
	config := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-config", proxy.Name),
			Namespace: proxy.Namespace,
		},
		Data: data,
	}
	return config
}

func readMap(path string, proxy api.ProxyService) map[string]string {
	result := map[string]string{}
	paths, _ := ioutil.ReadDir(path)
	services := Services{
		Upstreams: map[string]string{},
		Mappings:  map[string]string{},
	}
	for count, service := range proxy.Spec.Services {
		if count == 0 {
			services.Mappings["default"] = service
		} else {
			services.Mappings[service] = service
		}
		services.Upstreams[service] = service
	}
	for _, file := range paths {
		name := file.Name()
		if strings.HasSuffix(name, ".tmpl") {
			arr, err := ioutil.ReadFile(path + "/" + file.Name())
			if err == nil {
				value := string(arr)
				value = strings.TrimSuffix(value, "\n")
				var tmpl *template.Template
				tmpl, err = template.New(file.Name()).Parse(value)
				if err == nil {
					buffer := &bytes.Buffer{}
					err := tmpl.Execute(buffer, services)
					if err == nil {
						value := buffer.String() + "\n"
						key := strings.TrimSuffix(name, ".tmpl")
						result[key] = value
					}
				}
			}
		} else {
			arr, _ := ioutil.ReadFile(path + "/" + name)
			result[name] = strings.TrimSuffix(string(arr), "\n") + "\n"
		}
	}
	return result
}

// Services a convenience wrapper for the nginx config template
type Services struct {
	Upstreams map[string]string
	Mappings  map[string]string
}

func updateConfig(config *corev1.ConfigMap, proxy *api.ProxyService) *corev1.ConfigMap {
	return config
}

func createService(proxy *api.ProxyService) *corev1.Service {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Labels:    map[string]string{"proxy": proxy.Name},
			Name:      proxy.Name,
			Namespace: proxy.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				corev1.ServicePort{
					Protocol:   "TCP",
					Port:       80,
					TargetPort: intstr.FromInt(80),
					Name:       "http",
				},
			},
			Selector: map[string]string{"proxy": proxy.Name},
		},
	}
	return service
}

func updateService(service *corev1.Service, proxy *api.ProxyService) *corev1.Service {
	return service
}

func (r *ProxyServiceReconciler) constructService(proxy *api.ProxyService) (*corev1.Service, error) {
	service := createService(proxy)
	if err := ctrl.SetControllerReference(proxy, service, r.Scheme); err != nil {
		return nil, err
	}
	return service, nil
}

// SetupWithManager Utility method to set up manager
func (r *ProxyServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(&apps.Deployment{}, ownerKey, func(rawObj runtime.Object) []string {
		// grab the deployment object, extract the owner...
		deployment := rawObj.(*apps.Deployment)
		owner := metav1.GetControllerOf(deployment)
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
	if err := mgr.GetFieldIndexer().IndexField(&corev1.ConfigMap{}, ownerKey, func(rawObj runtime.Object) []string {
		// grab the service object, extract the owner...
		config := rawObj.(*corev1.ConfigMap)
		owner := metav1.GetControllerOf(config)
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
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Service{}).
		Owns(&apps.Deployment{}).
		Complete(r)
}
