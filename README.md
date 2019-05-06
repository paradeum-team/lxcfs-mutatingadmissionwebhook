# lxcfs-mutatingadmissionwebhook

## 简介
此项目使用Kubernetes admission webhooks，在pod创建之前将lxcfs相关目录挂在到容器内。


## 部署测试
>本项目部署在openshift环境上，如果使用k8s将脚本中的oc 改为 kubectl 即可

### 生成secrets

```
$ ./deployment/webhook-create-signed-cert.sh

creating certs in tmpdir /var/folders/3z/\_d8d8kl951ggyvw360dkd_y80000gn/T/tmp.xPApwE5H
Generating RSA private key, 2048 bit long modulus
..............................................+++
...........+++
e is 65537 (0x10001)
certificatesigningrequest.certificates.k8s.io "lxcfs-webhook-svc.default" created
NAME                                    AGE       REQUESTOR               CONDITION
admission-webhook-example-svc.default   1s        ekscluster-marton-423   Pending
certificatesigningrequest.certificates.k8s.io "lxcfs-webhook-svc.default" approved
secret "lxcfs-webhook-certs" created

$ oc get secret lxcfs-webhook-certs
NAME                              TYPE      DATA      AGE
lxcfs-webhook-certs   Opaque    2         2m
```

### 权限配置


- 创建角色，用户并绑定关系

``` 
oc  create -f ./deployment/service-account.yaml && oc create -f ./deployment/clusterrole.yaml  && oc create -f ./deployment/clusterrolebinding.yaml
```

- 创建scc

``` 
oc create -f ./deployment/lxcfs-webhook-scc.yaml --validate=false

```


### 创建deployment和service


```
$ oc create -f deployment/deployment.yaml
deployment.apps "lxcfs-webhook-deployment" created

$ oc create -f deployment/service.yaml
service "lxcfs-webhook-svc" created

```

### 配置webhook 


```
$ cat ./deployment/mutatingwebhook.yaml | ./deployment/webhook-patch-ca-bundle.sh > ./deployment/mutatingwebhook-ca-bundle.yaml

$ kubectl create -f deployment/mutatingwebhook-ca-bundle.yaml
mutatingwebhookconfiguration.admissionregistration.k8s.io "lxcfs-webhook-cfg" created

```

### 标记namespace


```
$ kubectl label namespace default lxcfs-webhook=enabled
namespace "default" labeled
```

### 测试（webhook将会自动挂载lxcfs相关目录）

```
$ kubectl create -f deployment/sleep.yaml

```

### 黑名单和白名单模式
>项目支持黑白名单模式，在deployment中配置环境变量 ‘BLACK_OR_WHITE’  ，BLACK为黑名单模式，WHITE 为白名单模式，默认为黑名单模式。

```
 env:
 - name: BLACK_OR_WHITE
   value: BLACK
```
>黑名单模式下，应用带有 lxcfs-webhook.paradeum.com/mutate=false 注解，webhook将不进行修改


>白名单模式下，应用带有 lxcfs-webhook.paradeum.com/mutate=true 注解，webhook将进行修改

## 参考文献
[Kubernetes 准入控制 Admission Controller 介绍](https://juejin.im/post/5ba3547ae51d450e425ec6a5)

[Diving into Kubernetes MutatingAdmissionWebhook](https://medium.com/ibm-cloud/diving-into-kubernetes-mutatingadmissionwebhook-6ef3c5695f74)




