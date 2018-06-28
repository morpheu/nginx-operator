package stub

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"github.com/tsuru/nginx-operator/pkg/apis/nginx/v1alpha1"
	"github.com/tsuru/nginx-operator/pkg/stub/k8s"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func NewHandler(logger *logrus.Logger) sdk.Handler {
	return &Handler{
		logger: logger,
	}
}

type Handler struct {
	logger *logrus.Logger
}

// Handle handles events for the operator
func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.Nginx:
		logger := h.logger.WithFields(map[string]interface{}{
			"name":      o.GetName(),
			"namespace": o.GetNamespace(),
			"kind":      o.GetObjectKind().GroupVersionKind().String(),
		})

		logger.Debugf("Handling event for object: %+v", o)

		if err := reconcile(ctx, event, o, logger); err != nil {
			return err
		}

		if err := refreshStatus(ctx, event, o, logger); err != nil {
			return err
		}

	}
	return nil
}

func reconcile(ctx context.Context, event sdk.Event, nginx *v1alpha1.Nginx, logger *logrus.Entry) error {
	if event.Deleted {
		// Do nothing because garbage collector will remove created resources using the OwnerReference.
		// All secondary resources must have the CR set as their OwnerReference for this to be the case
		logger.Info("object deleted")
		return nil
	}

	if err := reconcileDeployment(ctx, nginx, logger); err != nil {
		return err
	}

	if err := reconcileService(ctx, nginx, logger); err != nil {
		return err
	}

	return nil
}

func reconcileDeployment(ctx context.Context, nginx *v1alpha1.Nginx, logger *logrus.Entry) error {
	deployment := k8s.NewDeployment(nginx)

	err := sdk.Create(deployment)
	if err != nil && !errors.IsAlreadyExists(err) {
		logger.Errorf("Failed to create deployment: %v", err)
		return err
	}

	if err == nil {
		return nil
	}

	if err := sdk.Get(deployment); err != nil {
		logger.Errorf("Failed to retrieve deployment: %v", err)
		return err
	}

	// TODO: reconcile deployment fields with nginx fields
	// call sdk.Update if there were any changes
	var changed bool
	if !changed {
		logger.Debug("nothing changed")
		return nil
	}

	if err := sdk.Update(deployment); err != nil {
		logger.Errorf("Failed to update deployment: %v", err)
		return err
	}

	return nil
}

func reconcileService(ctx context.Context, nginx *v1alpha1.Nginx, logger *logrus.Entry) error {
	service := k8s.NewService(nginx)

	err := sdk.Create(service)
	if errors.IsAlreadyExists(err) {
		return nil
	}

	if err != nil {
		logger.Errorf("Failed to create service: %v", err)
	}

	return err
}

func refreshStatus(ctx context.Context, event sdk.Event, nginx *v1alpha1.Nginx, logger *logrus.Entry) error {
	if event.Deleted {
		logger.Debug("nginx deleted, skipping status update")
		return nil
	}

	podList := &corev1.PodList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
	}

	labelSelector := labels.SelectorFromSet(k8s.LabelsForNginx(nginx.Name)).String()
	listOps := &metav1.ListOptions{LabelSelector: labelSelector}
	err := sdk.List(nginx.Namespace, podList, sdk.WithListOptions(listOps))
	if err != nil {
		return fmt.Errorf("failed to list pods: %v", err)
	}

	var pods []v1alpha1.NginxPod
	for _, p := range podList.Items {
		pods = append(pods, v1alpha1.NginxPod{Name: p.Name, PodIP: p.Status.PodIP})
	}
	sort.Slice(pods, func(i, j int) bool {
		return pods[i].Name < pods[j].Name
	})
	sort.Slice(nginx.Status.Pods, func(i, j int) bool {
		return nginx.Status.Pods[i].Name < nginx.Status.Pods[j].Name
	})
	if !reflect.DeepEqual(pods, nginx.Status.Pods) {
		nginx.Status.Pods = pods
		err := sdk.Update(nginx)
		if err != nil {
			return fmt.Errorf("failed to update nginx status: %v", err)
		}
	}

	return nil
}
