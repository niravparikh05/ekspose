package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	fmt.Println("Let's begin ekspose - custom k8s controller")

	//STEP 1: Get the kubeconfig that holds all information required to interact with kube api / cluster
	kubeconfig := flag.String("kubeconfig", "/home/infracloud/.kube/config", "absolute path to the kubeconfig file")
	flag.Parse()

	//STEP 2: Build the configuration from kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		fmt.Println("Unable to build kube configuration ", err.Error())
		config, err = rest.InClusterConfig()
		if err != nil {
			log.Fatalf("Error %s, getting incluster config", err.Error())
		}
	}

	//STEP 3: build a client set that has access to all resource api versions
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalln("Unable to build kube clientset ", err.Error())
	}

	//STEP 4: Create an informer and run
	stopCh := make(chan struct{})
	defer close(stopCh)
	sharedInformerFactory := informers.NewSharedInformerFactory(clientset, 10*time.Minute)

	//STEP 5: create the controller and start
	controller := newController(clientset, sharedInformerFactory.Apps().V1().Deployments())
	sharedInformerFactory.Start(stopCh)
	if err := controller.start(stopCh); err != nil {
		log.Fatalf("Error running controller %s", err.Error())
	}

	fmt.Println("application stopped !")

}
