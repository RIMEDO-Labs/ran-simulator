// SPDX-FileCopyrightText: 2020-present Open Networking Foundation <info@opennetworking.org>
//
// SPDX-License-Identifier: LicenseRef-ONF-Member-1.0

package kpm

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/onosproject/ran-simulator/pkg/store/subscriptions"

	kpmutils "github.com/onosproject/ran-simulator/pkg/utils/e2sm/kpm/indication"

	"github.com/onosproject/ran-simulator/pkg/model"

	"github.com/onosproject/ran-simulator/pkg/modelplugins"

	"github.com/onosproject/onos-e2-sm/servicemodels/e2sm_kpm/pdubuilder"
	indicationutils "github.com/onosproject/ran-simulator/pkg/utils/e2ap/indication"
	subutils "github.com/onosproject/ran-simulator/pkg/utils/e2ap/subscription"
	subdeleteutils "github.com/onosproject/ran-simulator/pkg/utils/e2ap/subscriptiondelete"

	"github.com/onosproject/onos-lib-go/pkg/logging"

	"github.com/onosproject/onos-lib-go/pkg/errors"
	"github.com/onosproject/ran-simulator/pkg/servicemodel/registry"

	"github.com/onosproject/onos-e2t/api/e2ap/v1beta1/e2apies"
	"github.com/onosproject/onos-e2t/api/e2ap/v1beta1/e2appducontents"
	"github.com/onosproject/onos-e2t/pkg/southbound/e2ap/types"
	"github.com/onosproject/ran-simulator/pkg/servicemodel"
	"google.golang.org/protobuf/proto"
)

var _ servicemodel.Client = &Client{}

var log = logging.GetLogger("sm", "kpm")

const (
	modelFullName = "e2sm_kpm-v1beta1"
	version       = "v1beta1"
)

// Client kpm service model client
type Client struct {
	Subscriptions *subscriptions.Subscriptions
	ServiceModel  *registry.ServiceModel
	Model         *model.Model
}

// NewServiceModel creates a new service model
func NewServiceModel(node model.Node, model *model.Model, modelPluginRegistry *modelplugins.ModelPluginRegistry) (registry.ServiceModel, error) {
	modelFullName := modelplugins.ModelFullName(modelFullName)
	kpmSm := registry.ServiceModel{
		RanFunctionID:       registry.Kpm,
		ModelFullName:       modelFullName,
		Client:              &Client{},
		Revision:            1,
		Version:             version,
		ModelPluginRegistry: modelPluginRegistry,
		Node:                node,
		Model:               model,
	}
	var ranFunctionShortName = string(modelFullName)
	var ranFunctionE2SmOid = "OID123"
	var ranFunctionDescription = "KPM Monitor"
	var ranFunctionInstance int32 = 1
	var ricEventStyleType int32 = 1
	var ricEventStyleName = "Periodic report"
	var ricEventFormatType int32 = 5
	var ricReportStyleType int32 = 1
	var ricReportStyleName = "O-CU-CP Measurement Container for the 5GC connected deployment"
	var ricIndicationHeaderFormatType int32 = 1
	var ricIndicationMessageFormatType int32 = 1
	ranFuncDescPdu, err := pdubuilder.CreateE2SmKpmRanfunctionDescriptionMsg(ranFunctionShortName, ranFunctionE2SmOid, ranFunctionDescription,
		ranFunctionInstance, ricEventStyleType, ricEventStyleName, ricEventFormatType, ricReportStyleType, ricReportStyleName,
		ricIndicationHeaderFormatType, ricIndicationMessageFormatType)
	if err != nil {
		log.Error(err)
		return registry.ServiceModel{}, err
	}

	protoBytes, err := proto.Marshal(ranFuncDescPdu)
	if err != nil {
		log.Error(err)
		return registry.ServiceModel{}, err
	}
	kpmModelPlugin := modelPluginRegistry.ModelPlugins[modelFullName]
	if kpmModelPlugin == nil {
		return registry.ServiceModel{}, errors.New(errors.Invalid, "model plugin is nil")
	}
	ranFuncDescBytes, err := kpmModelPlugin.RanFuncDescriptionProtoToASN1(protoBytes)
	if err != nil {
		log.Error(err)
		return registry.ServiceModel{}, err
	}

	kpmSm.Description = ranFuncDescBytes
	return kpmSm, nil
}

func (sm *Client) reportIndication(ctx context.Context, interval int32, subscription *subutils.Subscription) error {
	subID := subscriptions.NewID(subscription.GetRicInstanceID(), subscription.GetReqID(), subscription.GetRanFuncID())
	gNbID, err := strconv.ParseUint(fmt.Sprintf("%d", sm.ServiceModel.Node.EnbID), 10, 64)
	if err != nil {
		log.Error(err)
		return err
	}
	// Creates an indication header
	header, _ := kpmutils.NewIndicationHeader(
		kpmutils.WithPlmnID(fmt.Sprintf("%d", sm.ServiceModel.Model.PlmnID)),
		kpmutils.WithGnbID(gNbID),
		kpmutils.WithSst("1"),
		kpmutils.WithSd("SD1"),
		kpmutils.WithPlmnIDnrcgi(fmt.Sprintf("%d", sm.ServiceModel.Model.PlmnID)))

	kpmModelPlugin := sm.ServiceModel.ModelPluginRegistry.ModelPlugins[sm.ServiceModel.ModelFullName]
	indicationHeaderAsn1Bytes, err := kpmutils.CreateIndicationHeaderAsn1Bytes(kpmModelPlugin, header)
	if err != nil {
		log.Error(err)
		return err
	}

	// Creating an indication message
	indMsg, err := kpmutils.NewIndicationMessage(
		kpmutils.WithNumberOfActiveUes(int32(sm.Model.UEs.GetNumUes())))
	if err != nil {
		log.Error(err)
		return err
	}

	indicationMessageBytes, err := kpmutils.CreateIndicationMessageAsn1Bytes(kpmModelPlugin, indMsg)
	if err != nil {
		return err
	}

	intervalDuration := time.Duration(interval)
	sub, err := sm.Subscriptions.Get(subID)
	if err != nil {
		return err
	}
	sub.Ticker = time.NewTicker(intervalDuration * time.Millisecond)
	for range sub.Ticker.C {
		log.Debug("Sending Indication Report for subscription:", sub.ID)
		indication, _ := indicationutils.NewIndication(
			indicationutils.WithRicInstanceID(subscription.GetRicInstanceID()),
			indicationutils.WithRanFuncID(subscription.GetRanFuncID()),
			indicationutils.WithRequestID(subscription.GetReqID()),
			indicationutils.WithIndicationHeader(indicationHeaderAsn1Bytes),
			indicationutils.WithIndicationMessage(indicationMessageBytes))

		ricIndication := indicationutils.CreateIndication(indication)
		err = sub.E2Channel.RICIndication(ctx, ricIndication)
		if err != nil {
			log.Error("Sending indication report is failed:", err)
			return err
		}
	}
	return nil
}

// RICControl implements control handler for kpm service model
func (sm *Client) RICControl(ctx context.Context, request *e2appducontents.RiccontrolRequest) (response *e2appducontents.RiccontrolAcknowledge, failure *e2appducontents.RiccontrolFailure, err error) {
	return nil, nil, errors.New(errors.NotSupported, "Control operation is not supported")
}

// RICSubscription implements subscription handler for kpm service model
func (sm *Client) RICSubscription(ctx context.Context, request *e2appducontents.RicsubscriptionRequest) (response *e2appducontents.RicsubscriptionResponse, failure *e2appducontents.RicsubscriptionFailure, err error) {
	log.Info("RIC Subscription request received for service model:", sm.ServiceModel.ModelFullName)
	var ricActionsAccepted []*types.RicActionID
	ricActionsNotAdmitted := make(map[types.RicActionID]*e2apies.Cause)
	actionList := subutils.GetRicActionToBeSetupList(request)
	reqID := subutils.GetRequesterID(request)
	ranFuncID := subutils.GetRanFunctionID(request)
	ricInstanceID := subutils.GetRicInstanceID(request)

	for _, action := range actionList {
		actionID := types.RicActionID(action.Value.RicActionId.Value)
		actionType := action.Value.RicActionType
		// kpm service model supports report action and should be added to the
		// list of accepted actions
		if actionType == e2apies.RicactionType_RICACTION_TYPE_REPORT {
			ricActionsAccepted = append(ricActionsAccepted, &actionID)
		}
		// kpm service model does not support INSERT and POLICY actions and
		// should be added into the list of not admitted actions
		if actionType == e2apies.RicactionType_RICACTION_TYPE_INSERT ||
			actionType == e2apies.RicactionType_RICACTION_TYPE_POLICY {
			cause := &e2apies.Cause{
				Cause: &e2apies.Cause_RicRequest{
					RicRequest: e2apies.CauseRic_CAUSE_RIC_ACTION_NOT_SUPPORTED,
				},
			}
			ricActionsNotAdmitted[actionID] = cause
		}
	}
	subscription, _ := subutils.NewSubscription(
		subutils.WithRequestID(reqID),
		subutils.WithRanFuncID(ranFuncID),
		subutils.WithRicInstanceID(ricInstanceID),
		subutils.WithActionsAccepted(ricActionsAccepted),
		subutils.WithActionsNotAdmitted(ricActionsNotAdmitted))

	// At least one required action must be accepted otherwise sends a subscription failure response
	if len(ricActionsAccepted) == 0 {
		err := errors.New(errors.Forbidden, "no required action is accepted")
		subscriptionFailure := subutils.CreateSubscriptionFailure(subscription)
		return nil, subscriptionFailure, err
	}

	reportInterval, err := sm.getReportPeriod(request)
	if err != nil {
		subscriptionFailure := subutils.CreateSubscriptionFailure(subscription)
		return nil, subscriptionFailure, err
	}

	subscriptionResponse := subutils.CreateSubscriptionResponse(subscription)
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := sm.reportIndication(ctx, reportInterval, subscription)
		if err != nil {
			return
		}
	}()
	return subscriptionResponse, nil, nil

}

// RICSubscriptionDelete implements subscription delete handler for kpm service model
func (sm *Client) RICSubscriptionDelete(ctx context.Context, request *e2appducontents.RicsubscriptionDeleteRequest) (response *e2appducontents.RicsubscriptionDeleteResponse, failure *e2appducontents.RicsubscriptionDeleteFailure, err error) {
	log.Info("RIC subscription delete request is received for service model:", sm.ServiceModel.ModelFullName)
	reqID := subdeleteutils.GetRequesterID(request)
	ranFuncID := subdeleteutils.GetRanFunctionID(request)
	ricInstanceID := subdeleteutils.GetRicInstanceID(request)
	subID := subscriptions.NewID(ricInstanceID, reqID, ranFuncID)
	sub, err := sm.Subscriptions.Get(subID)
	if err != nil {
		return nil, nil, err
	}
	subscriptionDelete, _ := subdeleteutils.NewSubscriptionDelete(
		subdeleteutils.WithRequestID(reqID),
		subdeleteutils.WithRanFuncID(ranFuncID),
		subdeleteutils.WithRicInstanceID(ricInstanceID))
	subDeleteResponse := subdeleteutils.CreateSubscriptionDeleteResponse(subscriptionDelete)
	// Stops the goroutine sending the indication messages
	sub.Ticker.Stop()
	return subDeleteResponse, nil, nil
}
