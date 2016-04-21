/*
Licensed to the Apache Software Foundation (ASF) under one
or more contributor license agreements.  See the NOTICE file
distributed with this work for additional information
regarding copyright ownership.  The ASF licenses this file
to you under the Apache License, Version 2.0 (the
"License"); you may not use this file except in compliance
with the License.  You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing,
software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
KIND, either express or implied.  See the License for the
specific language governing permissions and limitations
under the License.
*/

package obcpbft

import (
	"fmt"

	"github.com/hyperledger/fabric/consensus"
	pb "github.com/hyperledger/fabric/protos"

	"github.com/golang/protobuf/proto"
	"github.com/spf13/viper"
)

type obcClassic struct {
	stack consensus.Stack
	pbft  *pbftCore

	persistForward
}

func newObcClassic(id uint64, config *viper.Viper, stack consensus.Stack) *obcClassic {
	op := &obcClassic{stack: stack}

	op.persistForward.persistor = stack

	logger.Debug("Replica %d obtaining startup information", id)

	op.pbft = newPbftCore(id, config, op)

	queueSize := config.GetInt("executor.queuesize")
	if queueSize <= int(op.pbft.L) {
		logger.Error("Replica %d has executor queue size %d less than PBFT log size %d, this indicates a misconfiguration", id, queueSize, op.pbft.L)
	}

	return op
}

// RecvMsg receives both CHAIN_TRANSACTION and CONSENSUS messages from
// the stack. New transaction requests are broadcast to all replicas,
// so that the current primary will receive the request.
func (op *obcClassic) RecvMsg(ocMsg *pb.Message, senderHandle *pb.PeerID) error {
	if ocMsg.Type == pb.Message_CHAIN_TRANSACTION {
		logger.Info("New consensus request received")

		req := &Request{Payload: ocMsg.Payload, ReplicaId: op.pbft.id}
		pbftMsg := &Message{&Message_Request{req}}
		packedPbftMsg, _ := proto.Marshal(pbftMsg)
		op.broadcast(packedPbftMsg)
		op.pbft.request(ocMsg.Payload, op.pbft.id)

		return nil
	}

	if ocMsg.Type != pb.Message_CONSENSUS {
		return fmt.Errorf("Unexpected message type: %s", ocMsg.Type)
	}

	senderID, err := getValidatorID(senderHandle)
	if err != nil {
		panic("Cannot map sender's PeerID to a valid replica ID")
	}

	op.pbft.receive(ocMsg.Payload, senderID)

	return nil
}

// StateUpdate is a signal from the stack that it has fast-forwarded its state
func (op *obcClassic) StateUpdate(id []byte) {
	op.pbft.stateUpdate(id)
}

// Close tells us to release resources we are holding
func (op *obcClassic) Close() {
	op.pbft.close()
}

// =============================================================================
// innerStack interface (functions called by pbft-core)
// =============================================================================

// multicast a message to all replicas
func (op *obcClassic) broadcast(msgPayload []byte) {
	ocMsg := &pb.Message{
		Type:    pb.Message_CONSENSUS,
		Payload: msgPayload,
	}
	op.stack.Broadcast(ocMsg, pb.PeerEndpoint_UNDEFINED)
}

// send a message to a specific replica
func (op *obcClassic) unicast(msgPayload []byte, receiverID uint64) (err error) {
	ocMsg := &pb.Message{
		Type:    pb.Message_CONSENSUS,
		Payload: msgPayload,
	}
	receiverHandle, err := getValidatorHandle(receiverID)
	if err != nil {
		return
	}
	return op.stack.Unicast(ocMsg, receiverHandle)
}

func (op *obcClassic) sign(msg []byte) ([]byte, error) {
	return op.stack.Sign(msg)
}

func (op *obcClassic) verify(senderID uint64, signature []byte, message []byte) error {
	senderHandle, err := getValidatorHandle(senderID)
	if err != nil {
		return err
	}
	return op.stack.Verify(senderHandle, signature, message)
}

// validate checks whether the request is valid syntactically
func (op *obcClassic) validate(txRaw []byte) error {
	tx := &pb.Transaction{}
	err := proto.Unmarshal(txRaw, tx)
	return err
}

// execute an opaque request which corresponds to an OBC Transaction
func (op *obcClassic) execute(seqNo uint64, txRaw []byte) {
	tx := &pb.Transaction{}
	err := proto.Unmarshal(txRaw, tx)
	if err != nil {
		logger.Error("Unable to unmarshal transaction: %v", err)
		return
	}

	id := []byte("foo")
	op.stack.BeginTxBatch(id)
	result, err := op.stack.ExecTxs(id, []*pb.Transaction{tx})
	_ = err    // XXX what to do on error?
	_ = result // XXX what to do with the result?
	_, err = op.stack.CommitTxBatch(id, nil)
}

// called when a view-change happened in the underlying PBFT
// classic mode pbft does not use this information
func (op *obcClassic) viewChange(curView uint64) {
}

func (op *obcClassic) Checkpoint(seqNo uint64, id []byte) {
	op.pbft.Checkpoint(seqNo, id)
}

func (op *obcClassic) skipTo(seqNo uint64, id []byte, replicas []uint64) {
	op.stack.SkipTo(seqNo, id, getValidatorHandles(replicas))
}

// Unnecessary
func (op *obcClassic) Validate(seqNo uint64, id []byte) (commit bool, correctedID []byte, peerIDs []*pb.PeerID) {
	return
}

func (op *obcClassic) getState() []byte {
	return op.stack.GetBlockchainInfoBlob()
}
