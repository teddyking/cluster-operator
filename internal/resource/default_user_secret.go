// RabbitMQ Cluster Operator
//
// Copyright 2020 VMware, Inc. All Rights Reserved.
//
// This product is licensed to you under the Mozilla Public license, Version 2.0 (the "License").  You may not use this product except in compliance with the Mozilla Public License.
//
// This product may include a number of subcomponents with separate copyright notices and license terms. Your use of these subcomponents is subject to the terms and conditions of the subcomponent's license, as noted in the LICENSE file.
//

package resource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	rabbitmqv1beta1 "github.com/rabbitmq/cluster-operator/api/v1beta1"
	"github.com/rabbitmq/cluster-operator/internal/metadata"
	"gopkg.in/ini.v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultUserSecretName = "default-user"
	BindingType           = "rabbitmq"
	BindingProvider       = "rabbitmq-cluster-operator"
)

type DefaultUserSecretBuilder struct {
	Client   client.Client
	Instance *rabbitmqv1beta1.RabbitmqCluster
	Scheme   *runtime.Scheme
}

func (builder *RabbitmqResourceBuilder) DefaultUserSecret() *DefaultUserSecretBuilder {
	return &DefaultUserSecretBuilder{
		Client:   builder.Client,
		Instance: builder.Instance,
		Scheme:   builder.Scheme,
	}
}

func (builder *DefaultUserSecretBuilder) Build() (runtime.Object, error) {
	username, err := randomEncodedString(24)
	if err != nil {
		return nil, err
	}

	password, err := randomEncodedString(24)
	if err != nil {
		return nil, err
	}

	defaultUserConf, err := generateDefaultUserConf(username, password)
	if err != nil {
		return nil, err
	}

	port := "5672"
	scheme := "amqp"

	//hacking in some k8s binding spec below
	host := fmt.Sprintf("%s.%s.svc", builder.Instance.ChildResourceName("client"), builder.Instance.Namespace)

	serviceName := builder.Instance.ChildResourceName("client")

	if builder.Instance.Spec.Service.Type == "LoadBalancer" {
		service := &corev1.Service{}
		err = builder.Client.Get(context.Background(), types.NamespacedName{Namespace: builder.Instance.Namespace, Name: serviceName}, service)
		if err != nil {
			return nil, err
		}

		if len(service.Status.LoadBalancer.Ingress) < 1 {
			return nil, errors.New("waiting for loadbalancer")
		}

		host = service.Status.LoadBalancer.Ingress[0].IP
	}

	klog.Info("---host is---", host)

	uri := url.URL{
		Scheme: scheme,
		User:   url.UserPassword(username, password),
		Host:   fmt.Sprintf("%s:%s", host, port),
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      builder.Instance.ChildResourceName(DefaultUserSecretName),
			Namespace: builder.Instance.Namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"type":              []byte(BindingType),
			"provider":          []byte(BindingProvider),
			"scheme":            []byte(scheme),
			"host":              []byte(host),
			"port":              []byte(port),
			"username":          []byte(username),
			"password":          []byte(password),
			"uri":               []byte(uri.String()),
			"default_user.conf": defaultUserConf,
		},
	}, nil
}

func (builder *DefaultUserSecretBuilder) Update(object runtime.Object) error {
	secret := object.(*corev1.Secret)
	secret.Labels = metadata.GetLabels(builder.Instance.Name, builder.Instance.Labels)
	secret.Annotations = metadata.ReconcileAndFilterAnnotations(secret.GetAnnotations(), builder.Instance.Annotations)

	if err := controllerutil.SetControllerReference(builder.Instance, secret, builder.Scheme); err != nil {
		return fmt.Errorf("failed setting controller reference: %v", err)
	}

	return nil
}

func generateDefaultUserConf(username, password string) ([]byte, error) {
	ini.PrettySection = false // Remove trailing new line because default_user.conf has only a default section.
	cfg, err := ini.Load([]byte{})
	if err != nil {
		return nil, err
	}
	defaultSection := cfg.Section("")

	if _, err := defaultSection.NewKey("default_user", username); err != nil {
		return nil, err
	}

	if _, err := defaultSection.NewKey("default_pass", password); err != nil {
		return nil, err
	}

	var userConfBuffer bytes.Buffer
	if _, err := cfg.WriteTo(&userConfBuffer); err != nil {
		return nil, err
	}

	return userConfBuffer.Bytes(), nil
}
