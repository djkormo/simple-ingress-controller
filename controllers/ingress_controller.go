package controllers

import (
	"context"
	"crypto/tls"
	"github.com/bep/debounce"
	"github.com/go-logr/logr"
	"github.com/rs/zerolog/log"
	networking "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"sync"
	"time"

	//	"encoding/json"
	//"reflect"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	//metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	//"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	//"sigs.k8s.io/controller-runtime/pkg/event"
	//	"sigs.k8s.io/controller-runtime/pkg/log"
	//"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// A Payload is a collection of Kubernetes data loaded by the watcher.
type Payload struct {
	Ingresses       []IngressPayload
	TLSCertificates map[string]*tls.Certificate
}

// An IngressPayload is an ingress + its service ports.
type IngressPayload struct {
	Ingress      *networking.Ingress
	ServicePorts map[string]map[string]int
}

// A Watcher watches for ingresses in the kubernetes cluster
type Watcher struct {
	client   kubernetes.Interface
	onChange func(*Payload)
}

type IngressReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// New creates a new Watcher.
func New(client kubernetes.Interface, onChange func(*Payload)) *Watcher {
	return &Watcher{
		client:   client,
		onChange: onChange,
	}
}

func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("Ingress", req.NamespacedName)

	/*
		Step 0: Fetch the Pod from the Kubernetes API.
	*/

	var ingress networkingv1.Ingress
	if err := r.Get(ctx, req.NamespacedName, &ingress); err != nil {
		if apierrors.IsNotFound(err) {
			// we'll ignore not-found errors, since they can't be fixed by an immediate
			// requeue (we'll need to wait for a new notification), and we can get them
			// on deleted requests.
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch Ingress")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		For(&corev1.Secret{}).
		For(&corev1.Service{}).
		Complete(r)
}

// Run runs the watcher.
// +kubebuilder:rbac:groups="",resources=ingresses,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (w *Watcher) Reconcile(ctx context.Context) error {
	factory := informers.NewSharedInformerFactory(w.client, time.Minute)
	secretLister := factory.Core().V1().Secrets().Lister()
	serviceLister := factory.Core().V1().Services().Lister()
	ingressLister := factory.Networking().V1().Ingresses().Lister()

	addBackend := func(ingressPayload *IngressPayload, backend networking.IngressBackend) {
		svc, err := serviceLister.Services(ingressPayload.Ingress.Namespace).Get(backend.Service.Name)
		if err != nil {
			log.Error().Err(err).
				Str("namespace", ingressPayload.Ingress.Namespace).
				Str("name", backend.Service.Name).
				Msg("unknown service")
		} else {
			m := make(map[string]int)
			for _, port := range svc.Spec.Ports {
				m[port.Name] = int(port.Port)
			}
			ingressPayload.ServicePorts[svc.Name] = m
		}
	}

	onChange := func() {
		payload := &Payload{
			TLSCertificates: make(map[string]*tls.Certificate),
		}

		ingresses, err := ingressLister.List(labels.Everything())
		if err != nil {
			log.Error().Err(err).Msg("failed to list ingresses")
			return
		}

		for _, ingress := range ingresses {
			ingressPayload := IngressPayload{
				Ingress:      ingress,
				ServicePorts: make(map[string]map[string]int),
			}
			payload.Ingresses = append(payload.Ingresses, ingressPayload)

			if ingress.Spec.DefaultBackend != nil {
				addBackend(&ingressPayload, *ingress.Spec.DefaultBackend)
			}
			for _, rule := range ingress.Spec.Rules {
				if rule.HTTP != nil {
					continue
				}
				for _, path := range rule.HTTP.Paths {
					addBackend(&ingressPayload, path.Backend)
				}
			}

			for _, rec := range ingress.Spec.TLS {
				if rec.SecretName != "" {
					secret, err := secretLister.Secrets(ingress.Namespace).Get(rec.SecretName)
					if err != nil {
						log.Error().
							Err(err).
							Str("namespace", ingress.Namespace).
							Str("name", rec.SecretName).
							Msg("unknown secret")
						continue
					}

					cert, err := tls.X509KeyPair(secret.Data["tls.crt"], secret.Data["tls.key"])
					if err != nil {
						log.Error().
							Err(err).
							Str("namespace", ingress.Namespace).
							Str("name", rec.SecretName).
							Msg("invalid tls certificate")
						continue
					}

					payload.TLSCertificates[rec.SecretName] = &cert
				}
			}
		}

		w.onChange(payload)
	}

	debounced := debounce.New(time.Second)
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			debounced(onChange)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			debounced(onChange)
		},
		DeleteFunc: func(obj interface{}) {
			debounced(onChange)
		},
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		informer := factory.Core().V1().Secrets().Informer()
		informer.AddEventHandler(handler)
		informer.Run(ctx.Done())
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		informer := factory.Networking().V1().Ingresses().Informer()
		informer.AddEventHandler(handler)
		informer.Run(ctx.Done())
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		informer := factory.Core().V1().Services().Informer()
		informer.AddEventHandler(handler)
		informer.Run(ctx.Done())
		wg.Done()
	}()

	wg.Wait()
	return nil
}
