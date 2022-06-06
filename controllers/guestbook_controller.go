/*
Copyright 2022.

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
	v1 "k8s.io/api/core/v1"
	v13 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	webappv1 "kubebuilderdemo/api/v1"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// GuestbookReconciler reconciles a Guestbook object
type GuestbookReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=webapp.houyazhen.com,resources=guestbooks,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=webapp.houyazhen.com,resources=guestbooks/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=webapp.houyazhen.com,resources=guestbooks/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Guestbook object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *GuestbookReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("guestbook", req.NamespacedName)
	logger.Info("start reconcile")
	// TODO(user): your logic here
	instance := &webappv1.Guestbook{}
	if err := r.Client.Get(ctx, req.NamespacedName, instance); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	lbs := labels.Set{"app": instance.Name}
	podList := &v1.PodList{}
	logger.Info(fmt.Sprintf("oldpodlist: %v", *podList))
	if err := r.Client.List(ctx, podList, &client.ListOptions{
		Namespace: req.Namespace, LabelSelector: labels.SelectorFromSet(lbs)}); err != nil {
		logger.Error(err, "fetching existing pods failed")
		return ctrl.Result{}, err
	}
	logger.Info(fmt.Sprintf("newpodlist: %v", *podList))

	var existPodNames []string
	logger.Info(fmt.Sprintf("oldexistpod: %s", existPodNames))
	for _, pod := range podList.Items {
		if pod.GetObjectMeta().GetDeletionTimestamp() != nil {
			continue
		}
		if pod.Status.Phase == v1.PodRunning || pod.Status.Phase == v1.PodPending {
			existPodNames = append(existPodNames, pod.GetObjectMeta().GetName())
		}
	}
	logger.Info(fmt.Sprintf("newexistpod: %s", existPodNames))

	currentStatus := webappv1.GuestbookStatus{
		Replicas: len(existPodNames),
		PodNames: existPodNames,
	}
	if !reflect.DeepEqual(instance.Status, currentStatus) {
		if currentStatus.PodNames != nil {
			instance.Status = currentStatus
			if err := r.Client.Status().Update(ctx, instance); err != nil {
				logger.Error(err, "update pod failed")
				return ctrl.Result{}, err
			}
		}
	}

	if instance.Spec.Replicas > len(existPodNames) {
		logger.Info(fmt.Sprintf("creating pod, current and expected num: %d %d", len(existPodNames), instance.Spec.Replicas))
		pod := newPodForCR(instance)
		if err := controllerutil.SetControllerReference(instance, pod, r.Scheme); err != nil {
			fmt.Println("scale up failed: SetControllerReference")
			return ctrl.Result{}, err
		}
		if err := r.Client.Create(ctx, pod); err != nil {
			logger.Info("scale up failed: create pod")
			return ctrl.Result{}, err
		}
	}

	if instance.Spec.Replicas < len(existPodNames) {
		logger.Info(fmt.Sprintf("deleting pod, current and expected num: %d %d", len(existPodNames), instance.Spec.Replicas))
		pod := podList.Items[0]
		podList.Items = podList.Items[1:]
		if err := r.Client.Delete(ctx, &pod); err != nil {
			logger.Info("scale down faled")
			return ctrl.Result{}, err
		}
	}

	if len(existPodNames) != 0 {
		serviceList := &v1.ServiceList{}
		l := labels.Set{"service": instance.Name}
		if err := r.Client.List(ctx, serviceList, &client.ListOptions{Namespace: instance.Namespace, LabelSelector: labels.SelectorFromSet(l)}); err != nil {
			logger.Error(err, "fetching existing service failed")
			return ctrl.Result{}, err
		}
		if len(serviceList.Items) == 0 {
			svc := newServiceForCR(instance)
			err := r.Client.Create(ctx, svc)
			if err != nil {
				logger.Info("create service failed")
				return ctrl.Result{}, err
			}
			logger.Info("create service successfully")
		} else {
			logger.Info("service  is exist")
		}
	} else if len(existPodNames) == 0 {
		serviceList := &v1.ServiceList{}
		l := labels.Set{"service": instance.Name}
		if err := r.Client.List(ctx, serviceList, &client.ListOptions{Namespace: instance.Namespace, LabelSelector: labels.SelectorFromSet(l)}); err != nil {
			logger.Error(err, "fetching existing service failed")
			return ctrl.Result{}, err
		}
		for _, v := range serviceList.Items {
			err := r.Client.Delete(ctx, &v)
			if err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("pod is 0,delete service successful")
		}
	}

	return ctrl.Result{Requeue: true}, nil
}

func newPodForCR(cr *webappv1.Guestbook) *v1.Pod {
	labels := map[string]string{"app": cr.Name}
	return &v1.Pod{
		ObjectMeta: v12.ObjectMeta{
			GenerateName: cr.Name + "-pod",
			Namespace:    cr.Namespace,
			Labels:       labels,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:    "busybox",
					Image:   "busybox",
					Command: []string{"sleep", "3600"},
				},
			},
		},
	}
}

func newServiceForCR(cr *webappv1.Guestbook) *v1.Service {
	return &v1.Service{
		ObjectMeta: v12.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    map[string]string{"service": cr.Name},
			OwnerReferences: []v12.OwnerReference{
				*v12.NewControllerRef(cr, v12.SchemeGroupVersion.WithKind("service")),
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name:     cr.Name,
					Port:     80,
					Protocol: v1.ProtocolTCP,
				},
			},
			Selector: map[string]string{
				"app": cr.Name,
			},
		},
	}

}

// SetupWithManager sets up the controller with the Manager.
func (r *GuestbookReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webappv1.Guestbook{}).
		Owns(&v1.Pod{}).
		Watches(&source.Kind{Type: &v1.Service{}}, &handler.EnqueueRequestForObject{}).
		Watches(&source.Kind{Type: &v13.Ingress{}}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}
