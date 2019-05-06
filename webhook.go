package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/golang/glog"
	"k8s.io/api/admission/v1beta1"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()
	// (https://github.com/kubernetes/kubernetes/issues/57982)
	defaulter = runtime.ObjectDefaulter(runtimeScheme)
)

var (
	ignoredNamespaces = []string{
		metav1.NamespaceSystem,
		metav1.NamespacePublic,
	}
)

const (
	admissionWebhookAnnotationMutateKey   = "lxcfs-webhook.paradeum.com/mutate"
	admissionWebhookAnnotationStatusKey   = "lxcfs-webhook.paradeum.com/status"

	envListTypeKey = "BLACK_OR_WHITE"

	blackList = "BLACK"
	whiteList = "WHITE"

	NA = "not_available"
)

type WebhookServer struct {
	server *http.Server
}
type config struct {
	volumes      []corev1.Volume
	volumeMounts []corev1.VolumeMount
}

// Webhook Server parameters
type WhSvrParameters struct {
	port           int    // webhook server port
	certFile       string // path to the x509 certificate for https
	keyFile        string // path to the x509 private key matching `CertFile`
	sidecarCfgFile string // path to sidecar injector configuration file
}

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func init() {
	_ = corev1.AddToScheme(runtimeScheme)
	_ = admissionregistrationv1beta1.AddToScheme(runtimeScheme)
	// defaulting with webhooks:
	// https://github.com/kubernetes/kubernetes/issues/57982
	_ = appsv1.AddToScheme(runtimeScheme)

}

func admissionRequired(ignoredList []string, admissionAnnotationKey string, metadata *metav1.ObjectMeta) bool {
	// skip special kubernetes system namespaces
	for _, namespace := range ignoredList {
		if metadata.Namespace == namespace {
			glog.Infof("Skip validation for %v for it's in special namespace:%v", metadata.Name, metadata.Namespace)
			return false
		}
	}
	annotations := metadata.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	var required bool
	//判断黑白名单模式
	blackOrWhite := os.Getenv(envListTypeKey)
	if blackOrWhite == "" {
		blackOrWhite = blackList
	}
	if blackOrWhite == blackList {
		switch strings.ToLower(annotations[admissionAnnotationKey]) {
		default:
			required = true
		case "n", "no", "false", "off":
			required = false
		}
		
	}else if blackOrWhite == whiteList{
		switch strings.ToLower(annotations[admissionAnnotationKey]) {
		default:
			required = false
		case "y", "yes", "true", "on":
			required = true
		}
	}
	return required
}

func mutationRequired(ignoredList []string, metadata *metav1.ObjectMeta) bool {
	required := admissionRequired(ignoredList, admissionWebhookAnnotationMutateKey, metadata)
	annotations := metadata.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	status := annotations[admissionWebhookAnnotationStatusKey]

	if strings.ToLower(status) == "mutated" {
		required = false
	}

	glog.Infof("Mutation policy for %v/%v: required:%v", metadata.Namespace, metadata.Name, required)
	return required
}

func updateAnnotation(target map[string]string, added map[string]string) (patch []patchOperation) {
	for key, value := range added {
		if target == nil || target[key] == "" {
			target = map[string]string{}
			patch = append(patch, patchOperation{
				Op:   "add",
				Path: "/metadata/annotations",
				Value: map[string]string{
					key: value,
				},
			})
		} else {
			patch = append(patch, patchOperation{
				Op:    "replace",
				Path:  "/metadata/annotations/" + key,
				Value: value,
			})
		}
	}
	return patch
}

func addVolume(target, added []corev1.Volume, basePath string) (patch []patchOperation) {
	first := len(target) == 0
	var value interface{}
	for _, add := range added {
		value = add
		path := basePath
		if first {
			first = false
			value = []corev1.Volume{add}
		} else {
			path = path + "/-"
		}
		patch = append(patch, patchOperation {
			Op:    "add",
			Path:  path,
			Value: value,
		})
	}
	return patch
}

func updateMounts(containers []corev1.Container,add []corev1.VolumeMount,basePath string) (patch []patchOperation)  {
	for i := range containers {
		containers[i].VolumeMounts = append(containers[i].VolumeMounts, add...)
	}
	patch = append(patch, patchOperation{
		Op:    "replace",
		Path:  basePath,
		Value: containers,
	})
	return patch
}

func createPatch(availableAnnotations map[string]string, annotations map[string]string,pod corev1.Pod) ([]byte, error) {
	c := &config{
		volumeMounts: []corev1.VolumeMount{
			corev1.VolumeMount{
				Name:      "lxcfs-proc-cpuinfo",
				MountPath: "/proc/cpuinfo",
			},
			corev1.VolumeMount{
				Name:      "lxcfs-proc-meminfo",
				MountPath: "/proc/meminfo",
			},
			corev1.VolumeMount{
				Name:      "lxcfs-proc-diskstats",
				MountPath: "/proc/diskstats",
			},
			corev1.VolumeMount{
				Name:      "lxcfs-proc-stat",
				MountPath: "/proc/stat",
			},
			corev1.VolumeMount{
				Name:      "lxcfs-proc-swaps",
				MountPath: "/proc/swaps",
			},
			corev1.VolumeMount{
				Name:      "lxcfs-proc-uptime",
				MountPath: "/proc/uptime",
			},
		},
		volumes: []corev1.Volume{
			corev1.Volume{
				Name: "lxcfs-proc-cpuinfo",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/var/lib/lxcfs/proc/cpuinfo",
					},
				},
			},
			corev1.Volume{
				Name: "lxcfs-proc-diskstats",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/var/lib/lxcfs/proc/diskstats",
					},
				},
			},
			corev1.Volume{
				Name: "lxcfs-proc-meminfo",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/var/lib/lxcfs/proc/meminfo",
					},
				},
			},
			corev1.Volume{
				Name: "lxcfs-proc-stat",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/var/lib/lxcfs/proc/stat",
					},
				},
			},
			corev1.Volume{
				Name: "lxcfs-proc-swaps",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/var/lib/lxcfs/proc/swaps",
					},
				},
			},
			corev1.Volume{
				Name: "lxcfs-proc-uptime",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/var/lib/lxcfs/proc/uptime",
					},
				},
			},
		},
	}
	var patch []patchOperation
	patch = append(patch, updateAnnotation(availableAnnotations, annotations)...)
	patch = append(patch, updateMounts(pod.Spec.Containers,c.volumeMounts,"/spec/containers")...)
	patch = append(patch, addVolume(pod.Spec.Volumes, c.volumes, "/spec/volumes")...)
	return json.Marshal(patch)
}


func (whsvr *WebhookServer) mutate(ar *v1beta1.AdmissionReview) *v1beta1.AdmissionResponse{
	req := ar.Request
	var (
		availableAnnotations map[string]string
		objectMeta                            *metav1.ObjectMeta
		resourceNamespace, resourceName       string
		pod corev1.Pod
	)

	glog.Infof("AdmissionReview for Kind=%v, Namespace=%v Name=%v (%v) UID=%v patchOperation=%v UserInfo=%v",
		req.Kind, req.Namespace, req.Name, resourceName, req.UID, req.Operation, req.UserInfo)

	if req.Kind.Kind == "Pod" {

		if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
			glog.Errorf("Could not unmarshal raw object: %v", err)
			return &v1beta1.AdmissionResponse{
				Result: &metav1.Status{
					Message: err.Error(),
				},
			}
		}
		resourceName, resourceNamespace, objectMeta = pod.Name, pod.Namespace, &pod.ObjectMeta
	}
	if !mutationRequired(ignoredNamespaces, objectMeta) {
		glog.Infof("Skipping validation for %s/%s due to policy check", resourceNamespace, resourceName)
		return &v1beta1.AdmissionResponse{
			Allowed: true,
		}
	}

	annotations := map[string]string{admissionWebhookAnnotationStatusKey: "mutated"}

	patchBytes, err := createPatch(availableAnnotations, annotations, pod)

	if err != nil {
		return &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	glog.Infof("AdmissionResponse: patch=%v\n", string(patchBytes))
	return &v1beta1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *v1beta1.PatchType {
			pt := v1beta1.PatchTypeJSONPatch
			return &pt
		}(),
	}
}

// Serve method for webhook server
func (whsvr *WebhookServer) serve(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	if len(body) == 0 {
		glog.Error("empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		glog.Errorf("Content-Type=%s, expect application/json", contentType)
		http.Error(w, "invalid Content-Type, expect `application/json`", http.StatusUnsupportedMediaType)
		return
	}
	var admissionResponse *v1beta1.AdmissionResponse
	ar := v1beta1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &ar); err != nil {
		glog.Errorf("Can't decode body: %v", err)
		admissionResponse = &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	} else {
		admissionResponse = whsvr.mutate(&ar)
	}
	admissionReview := v1beta1.AdmissionReview{}
	if admissionResponse != nil {
		admissionReview.Response = admissionResponse
		if ar.Request != nil {
			admissionReview.Response.UID = ar.Request.UID
		}
	}
	resp, err := json.Marshal(admissionReview)
	if err != nil {
		glog.Errorf("Can't encode response: %v", err)
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
	}
	glog.Infof("Ready to write reponse ...")
	if _, err := w.Write(resp); err != nil {
		glog.Errorf("Can't write response: %v", err)
		http.Error(w, fmt.Sprintf("could not write response: %v", err), http.StatusInternalServerError)
	}
}
