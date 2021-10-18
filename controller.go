package main

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformer "k8s.io/client-go/informers/apps/v1"
	"k8s.io/client-go/kubernetes"
	appslister "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type controller struct {
	clientset   kubernetes.Interface
	lister      appslister.DeploymentLister
	cacheSynced cache.InformerSynced
	queue       workqueue.RateLimitingInterface
}

func newController(clientset kubernetes.Interface, depInformer appsinformer.DeploymentInformer) *controller {
	objController := &controller{
		clientset:   clientset,
		lister:      depInformer.Lister(),
		cacheSynced: depInformer.Informer().HasSynced,
		queue:       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ekspose"),
	}

	depInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    objController.handleAdd,
		DeleteFunc: objController.handleDelete,
	})

	return objController
}

func (objController *controller) start(stopCh <-chan struct{}) error {
	fmt.Println("Ekspose controller started .. ")
	defer utilruntime.HandleCrash()
	defer objController.queue.ShutDown()

	// wait for controller to sync all the deployments in informer cache
	if ok := cache.WaitForCacheSync(stopCh, objController.cacheSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	go wait.Until(objController.worker, time.Second, stopCh)
	<-stopCh

	return nil
}

func (objController *controller) worker() {
	fmt.Println("Worker running .. ")
	for objController.processDeployment() {

	}

}

func (objController *controller) processDeployment() bool {
	fmt.Println("Process deployment ")
	//check for any deployments that are created to be processed
	item, shutdown := objController.queue.Get()
	if shutdown {
		return false
	}
	defer objController.queue.Forget(item)

	key, err := cache.MetaNamespaceKeyFunc(item)
	if err != nil {
		fmt.Printf("Error while fetching namespace key %s\n", err.Error())
		return false
	}
	ns, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		fmt.Printf("Error while splitting namespace key %s\n", err.Error())
		return false
	}

	//check if the deployment is deleted
	_, err = objController.clientset.AppsV1().Deployments(ns).Get(context.Background(), name, v1.GetOptions{})
	if apierrors.IsNotFound(err) {
		success, err := objController.processDeleteDeployment(ns, name)
		if err != nil {
			fmt.Printf("Error while processing deployment %s\n", err.Error())
			return false
		}
		return success

	} else {
		//otherwise handle create
		err = objController.createService(ns, name)
		if err != nil {
			fmt.Printf("Error while processing deployment %s\n", err.Error())
			return false
		}
		return true
	}

}

func (objController *controller) processDeleteDeployment(ns, name string) (bool, error) {
	fmt.Println("Process deleted deployment ", name)
	ctx := context.Background()
	err := objController.deleteService(ctx, ns, name)
	if err != nil {
		fmt.Printf("Error while deleting service %s\n", err.Error())
		return false, err
	}
	err = objController.deleteIngress(ctx, ns, name)
	if err != nil {
		fmt.Printf("Error while deleting ingress %s\n", err.Error())
		return false, err
	}
	return true, nil
}

func (objController *controller) createService(ns, name string) error {
	//here we need to create a service for this deployment

	//STEP 1: Get the deployment using ns, name from listers
	deployment, err := objController.lister.Deployments(ns).Get(name)
	if err != nil {
		fmt.Printf("Error while fetching deployment from informer lister %s\n", err.Error())
	}

	svc := corev1.Service{
		TypeMeta: v1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      deployment.Name,
			Namespace: deployment.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: deployment.Labels,
			Type:     corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:     "http",
					Protocol: corev1.ProtocolTCP,
					Port:     80,
				},
			},
		},
	}

	ctx := context.Background()
	_, err = objController.clientset.CoreV1().Services(ns).Create(ctx, &svc, v1.CreateOptions{})
	if err != nil {
		fmt.Printf("Error while creating service for deployment %s\n", err.Error())
	} else {
		fmt.Printf("Service %s successfully created\n", name)
		//create an ingress resource
		err = objController.createIngress(ctx, ns, name)
		if err != nil {
			fmt.Printf("Error while creating ingress for deployment %s\n", err.Error())
		} else {
			fmt.Printf("Ingress %s successfully created\n", name)
		}
	}
	return nil
}

func (objController *controller) createIngress(ctx context.Context, ns, name string) error {
	pathType := "Prefix"
	ingress := netv1.Ingress{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/rewrite-target": "/",
			},
		},
		Spec: netv1.IngressSpec{
			Rules: []netv1.IngressRule{
				{
					IngressRuleValue: netv1.IngressRuleValue{
						HTTP: &netv1.HTTPIngressRuleValue{
							Paths: []netv1.HTTPIngressPath{
								{
									Path:     fmt.Sprintf("/%s", name),
									PathType: (*netv1.PathType)(&pathType),
									Backend: netv1.IngressBackend{
										Service: &netv1.IngressServiceBackend{
											Name: name,
											Port: netv1.ServiceBackendPort{
												Number: 80,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	_, err := objController.clientset.NetworkingV1().Ingresses(ns).Create(ctx, &ingress, v1.CreateOptions{})
	return err
}

func (objController *controller) deleteService(ctx context.Context, ns, name string) error {
	return objController.clientset.CoreV1().Services(ns).Delete(ctx, name, v1.DeleteOptions{})
}

func (objController *controller) deleteIngress(ctx context.Context, ns, name string) error {
	return objController.clientset.NetworkingV1().Ingresses(ns).Delete(ctx, name, v1.DeleteOptions{})
}

func (objController *controller) handleAdd(obj interface{}) {
	fmt.Println("Deployment created ")
	objController.queue.Add(obj)
}

func (objController *controller) handleDelete(obj interface{}) {
	fmt.Println("Deployment deleted ")
	objController.queue.Add(obj)
}
