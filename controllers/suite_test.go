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
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	api "github.com/dsyer/spring-service-operator/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controllers Suite")
}

var _ = Describe("ProxyServiceReconciler", func() {
	const (
		testProxyService            = "myproxy"
		testNamespace               = "namespace"
	)

	var (
		reconciler *ProxyServiceReconciler
		req        ctrl.Request
		get        func(context.Context, client.ObjectKey, runtime.Object) error
		create     func(context.Context, runtime.Object, ...client.CreateOption) error
		update     func(context.Context, runtime.Object, ...client.UpdateOption) error
		list       func(context.Context, runtime.Object, ...client.ListOption) error
		status     func() client.StatusWriter
		result     ctrl.Result
		scheme	   *runtime.Scheme
		err        error
		proxy      *api.ProxyService
		// testErr    error
	)

	BeforeEach(func() {
		proxy = &api.ProxyService{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "spring.io/v1",
				Kind: "ProxyService",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:       testProxyService,
				Namespace:  testNamespace,
			},
		}
		scheme = runtime.NewScheme()
		api.AddToScheme(scheme)
		reconciler = &ProxyServiceReconciler{
			Client: testClient{
				get: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
					return get(ctx, key, obj)
				},
				update: func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
					return update(ctx, obj, opts...)
				},
				create: func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
					return create(ctx, obj, opts...)
				},
				list: func(ctx context.Context, obj runtime.Object, opts ...client.ListOption) error {
					return list(ctx, obj, opts...)
				},
				status: func() client.StatusWriter {
					return status()
				},
			},
			Log: ctrl.Log.WithName("testing"),
			Scheme: scheme,
			Recorder: testRecorder{},
		}
		req = ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: testNamespace,
				Name:      testProxyService,
			},
		}
		// testErr = errors.New("epic fail")
	})

	AfterEach(func() {
	})

	JustBeforeEach(func() {
		result, err = reconciler.Reconcile(req)
	})

	Context("when the proxy named by the request exists", func() {
		var (
			listErr           error
			listCalls         int
			updateErr         error
			updateCalls       int
			createErr         error
			createCalls       int
			statusUpdateErr   error
			statusUpdateCalls int
		)

		BeforeEach(func() {
			get = func(ctx context.Context, objectKey client.ObjectKey, out runtime.Object) error {
				Expect(objectKey).To(Equal(req.NamespacedName))
				outVal := reflect.ValueOf(out)
				reflect.Indirect(outVal).Set(reflect.Indirect(reflect.ValueOf(proxy)))
				return nil
			}
			updateErr = nil
			updateCalls = 0
			update = func(_ context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
				updateCalls++
				var ok bool
				proxy, ok = obj.(*api.ProxyService)
				Expect(ok).To(BeTrue())
				Expect(opts).To(BeEmpty())
				return updateErr
			}
			create = func(_ context.Context, obj runtime.Object, opts ...client.CreateOption) error {
				createCalls++
				return createErr
			}
			list = func(_ context.Context, obj runtime.Object, opts ...client.ListOption) error {
				listCalls++
				return listErr
			}
			statusUpdateErr = nil
			statusUpdateCalls = 0
			status = func() client.StatusWriter {
				update := func(_ context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
					statusUpdateCalls++
					updatedStatusImageMap, ok := obj.(*api.ProxyService)
					Expect(ok).To(BeTrue())
					proxy.Status = updatedStatusImageMap.Status
					Expect(opts).To(BeEmpty())
					return statusUpdateErr
				}
				patch := func(ctx context.Context, obj runtime.Object, patch client.Patch, opts ...client.PatchOption) error {
					return nil
				}
				return newStatusWriter(update, patch)
			}
		})

		It("should succeed and not be requeued", func() {
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
			Expect(createCalls).To(Equal(3))
			Expect(listCalls).To(Equal(3))
			Expect(statusUpdateCalls).To(Equal(1))
		})

	})

	Context("when the image map named by the request is not found", func() {
		var notFoundErr error

		BeforeEach(func() {
			notFoundErr = apierrs.NewNotFound(schema.GroupResource{Group: api.GroupVersion.Group, Resource: "ProxyService"}, testProxyService)
			get = func(ctx context.Context, objectKey client.ObjectKey, out runtime.Object) error {
				return notFoundErr
			}
		})

		It("should succeed", func() {
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

type testRecorder struct {
}

func (r testRecorder) Event(object runtime.Object, eventtype, reason, message string) {}

func (r testRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {}

func (r testRecorder) PastEventf(object runtime.Object, timestamp metav1.Time, eventtype, reason, messageFmt string, args ...interface{}) {}

func (r testRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {}

type testClient struct {
	get    func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error
	list   func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error
	create func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error
	delete func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error
	deleteAllOf func(ctx context.Context, obj runtime.Object, opts ...client.DeleteAllOfOption) error
	update func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error
	patch func(ctx context.Context, patch client.Patch, opts ...client.PatchOption) error
	status func() client.StatusWriter
}

func (c testClient) Get(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
	return c.get(ctx, key, obj)
}

func (c testClient) List(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
	return c.list(ctx, list, opts...)
}

func (c testClient) Update(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
	return c.update(ctx, obj, opts...)
}

func (c testClient) Patch(ctx context.Context, obj runtime.Object, patch client.Patch, opts ...client.PatchOption) error {
	return c.patch(ctx, patch, opts...)
}

func (c testClient) Delete(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
	return c.delete(ctx, obj, opts...)
}

func (c testClient) DeleteAllOf(ctx context.Context, obj runtime.Object, opts ...client.DeleteAllOfOption) error {
	return c.deleteAllOf(ctx, obj, opts...)
}

func (c testClient) Create(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
	return c.create(ctx, obj, opts...)
}

func (c testClient) Status() client.StatusWriter {
	return c.status()
}

type testStatusWriter struct {
	update func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error
	patch  func(ctx context.Context, obj runtime.Object, patch client.Patch, opts ...client.PatchOption) error
}

func (w testStatusWriter) Update(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
	return w.update(ctx, obj, opts...)
}

func (w testStatusWriter) Patch(ctx context.Context, obj runtime.Object, patch client.Patch, opts ...client.PatchOption) error {
	return w.patch(ctx, obj, patch, opts...)
}

func newStatusWriter(update func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error, patch func(ctx context.Context, obj runtime.Object, patch client.Patch, opts ...client.PatchOption) error) client.StatusWriter {
	return testStatusWriter{
		update: update,
		patch:  patch,
	}
}