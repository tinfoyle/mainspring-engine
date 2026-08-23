\set ON_ERROR_STOP on
BEGIN;
SELECT set_config('app.account_id','82100000-0000-4000-8000-000000000001',true);

SELECT set_config('spyglass.erasure_request_id','82ff0000-0000-4000-8000-0000000000ff',true);
DELETE FROM spyglass.agent_invocations WHERE account_id='82100000-0000-4000-8000-000000000001';
DELETE FROM spyglass.account_namespaces WHERE account_id='82100000-0000-4000-8000-000000000001';
SELECT set_config('spyglass.erasure_request_id','',true);
INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
VALUES ('82100000-0000-4000-8000-000000000001',1,'active',transaction_timestamp());

INSERT INTO spyglass.agent_boardrooms(account_id,id,name,purpose,state,version,created_at,updated_at)
VALUES ('82100000-0000-4000-8000-000000000001','83100000-0000-4000-8000-000000000001','Integration room','Verify delivery authority','active',1,transaction_timestamp(),transaction_timestamp());
INSERT INTO spyglass.agent_personas(account_id,id,boardroom_id,state,latest_version,created_at,updated_at)
VALUES ('82100000-0000-4000-8000-000000000001','83200000-0000-4000-8000-000000000002','83100000-0000-4000-8000-000000000001','active',1,transaction_timestamp(),transaction_timestamp());
INSERT INTO spyglass.agent_persona_versions(account_id,id,persona_id,version,name,role,description,system_instructions,policy,content_digest,created_by,created_at)
VALUES ('82100000-0000-4000-8000-000000000001','83300000-0000-4000-8000-000000000003','83200000-0000-4000-8000-000000000002',1,'Marketing Agent','Marketing','Integration fixture','Propose an exact Marketing release.','{}',decode(repeat('61',32),'hex'),'82300000-0000-4000-8000-000000000003',transaction_timestamp());
INSERT INTO spyglass.agent_conversations(account_id,id,boardroom_id,subject,state,next_message_sequence,created_by,created_at,updated_at)
VALUES ('82100000-0000-4000-8000-000000000001','83400000-0000-4000-8000-000000000004','83100000-0000-4000-8000-000000000001','Integration delivery','open',1,'82300000-0000-4000-8000-000000000003',transaction_timestamp(),transaction_timestamp());
INSERT INTO spyglass.agent_runs(account_id,id,boardroom_id,conversation_id,state,entitlement_version,policy_version,plan_digest,turn_count,created_by,created_at,started_at,completed_at)
VALUES ('82100000-0000-4000-8000-000000000001','83500000-0000-4000-8000-000000000005','83100000-0000-4000-8000-000000000001','83400000-0000-4000-8000-000000000004','succeeded',1,1,decode(repeat('62',32),'hex'),1,'82300000-0000-4000-8000-000000000003',transaction_timestamp(),transaction_timestamp(),transaction_timestamp());
INSERT INTO spyglass.agent_run_plan_turns(account_id,run_id,turn,persona_id,persona_version_id,persona_digest)
VALUES ('82100000-0000-4000-8000-000000000001','83500000-0000-4000-8000-000000000005',1,'83200000-0000-4000-8000-000000000002','83300000-0000-4000-8000-000000000003',decode(repeat('61',32),'hex'));
INSERT INTO spyglass.agent_invocations(account_id,id,run_id,turn,persona_version_id,status,expected_provider,requested_model,response_model,provider_response_id,runner_result_digest,result_digest,result_payload,input_tokens,output_tokens,total_tokens,queued_at,started_at,completed_at)
VALUES ('82100000-0000-4000-8000-000000000001','83600000-0000-4000-8000-000000000006','83500000-0000-4000-8000-000000000005',1,'83300000-0000-4000-8000-000000000003','succeeded','openai','gpt-test','gpt-test','resp-integration',decode(repeat('63',32),'hex'),decode(repeat('64',32),'hex'),'{}',1,1,2,transaction_timestamp(),transaction_timestamp(),transaction_timestamp());

INSERT INTO spyglass.attention_consequential_approvals
  (account_id,id,operation_id,invocation_id,capability,canonical_payload,input_sha256,hash_version,evidence_sha256,proposer_kind,proposer_id,policy_version,require_independent_review,expires_at,state,decision,decision_reason,decided_by_user_id,decided_at,version,created_at,updated_at)
VALUES ('82100000-0000-4000-8000-000000000001','82400000-0000-4000-8000-000000000004','83700000-0000-4000-8000-000000000007','83600000-0000-4000-8000-000000000006','marketing.release.activate',
  convert_to('{"campaign_id":"82500000-0000-4000-8000-000000000005","campaign_version":2,"release_id":"82600000-0000-4000-8000-000000000006","release_version":2}','UTF8'),
  decode(repeat('64',32),'hex'),1,decode(repeat('65',32),'hex'),'workload','runner-invocation:83600000-0000-4000-8000-000000000006',1,true,
  transaction_timestamp()+interval '2 hours','approved','approve','approved exact release','82300000-0000-4000-8000-000000000003',transaction_timestamp(),2,transaction_timestamp(),transaction_timestamp());

INSERT INTO spyglass.marketing_campaigns(account_id,id,name,objective,audience,state,active_release_id,version,created_by_kind,created_by_id,origin,created_at,updated_at)
VALUES ('82100000-0000-4000-8000-000000000001','82500000-0000-4000-8000-000000000005','Launch','Announce the release','Current customers','draft',NULL,1,'user','82300000-0000-4000-8000-000000000003','human',transaction_timestamp(),transaction_timestamp());
INSERT INTO spyglass.marketing_campaign_channels(account_id,campaign_id,channel)
VALUES ('82100000-0000-4000-8000-000000000001','82500000-0000-4000-8000-000000000005','email');
INSERT INTO spyglass.marketing_assets(account_id,campaign_id,id,created_at)
VALUES ('82100000-0000-4000-8000-000000000001','82500000-0000-4000-8000-000000000005','83800000-0000-4000-8000-000000000008',transaction_timestamp());
INSERT INTO spyglass.marketing_asset_revisions
  (account_id,id,campaign_id,asset_id,revision,kind,title,media_type,content_reference,content_sha256,content_bytes,alternative_text,created_by_kind,created_by_id,origin,created_at)
VALUES ('82100000-0000-4000-8000-000000000001','83900000-0000-4000-8000-000000000009','82500000-0000-4000-8000-000000000005','83800000-0000-4000-8000-000000000008',1,'copy','Launch copy','text/plain',
  'marketing/launch/copy/v1',decode('1f824d1f8a37a2421489796952cd582b5f68e9fc99f856a0fad7acae1983bc4c','hex'),22,'','user','82300000-0000-4000-8000-000000000003','human',transaction_timestamp());
INSERT INTO spyglass.marketing_release_plans(account_id,id,campaign_id,campaign_version,name,state,approval_id,version,created_by_kind,created_by_id,origin,submitted_by_user_id,approved_by_user_id,created_at,updated_at)
VALUES ('82100000-0000-4000-8000-000000000001','82600000-0000-4000-8000-000000000006','82500000-0000-4000-8000-000000000005',1,'Initial release','approved','82400000-0000-4000-8000-000000000004',3,'user','82300000-0000-4000-8000-000000000003','human','82300000-0000-4000-8000-000000000003','82300000-0000-4000-8000-000000000003',transaction_timestamp(),transaction_timestamp());
INSERT INTO spyglass.marketing_release_channels(account_id,campaign_id,release_id,channel)
VALUES ('82100000-0000-4000-8000-000000000001','82500000-0000-4000-8000-000000000005','82600000-0000-4000-8000-000000000006','email');
INSERT INTO spyglass.marketing_release_assets(account_id,campaign_id,release_id,asset_revision_id)
VALUES ('82100000-0000-4000-8000-000000000001','82500000-0000-4000-8000-000000000005','82600000-0000-4000-8000-000000000006','83900000-0000-4000-8000-000000000009');
UPDATE spyglass.marketing_campaigns SET state='active',active_release_id='82600000-0000-4000-8000-000000000006',version=2,updated_at=transaction_timestamp()
WHERE account_id='82100000-0000-4000-8000-000000000001' AND id='82500000-0000-4000-8000-000000000005';

INSERT INTO spyglass.integration_connections
  (account_id,id,name,connector_kind,state,current_revision,credential_generation,version,created_by_user_id,created_at,updated_at)
VALUES ('82100000-0000-4000-8000-000000000001','82700000-0000-4000-8000-000000000007','Campaign email','email','pending',1,0,1,'82300000-0000-4000-8000-000000000003',transaction_timestamp(),transaction_timestamp());
INSERT INTO spyglass.integration_connection_revisions
  (account_id,id,connection_id,revision,capabilities,email_address,audience_reference,created_by_user_id,created_at)
VALUES ('82100000-0000-4000-8000-000000000001','82800000-0000-4000-8000-000000000008','82700000-0000-4000-8000-000000000007',1,ARRAY['email.send'],'launch@example.com','audience:customers-v1','82300000-0000-4000-8000-000000000003',transaction_timestamp());
INSERT INTO spyglass.integration_credentials
  (account_id,id,connection_id,generation,provider,reference_sha256,state,created_by_user_id,created_at,updated_at)
VALUES ('82100000-0000-4000-8000-000000000001','82900000-0000-4000-8000-000000000009','82700000-0000-4000-8000-000000000007',1,'mock_smtp',decode(repeat('71',32),'hex'),'active','82300000-0000-4000-8000-000000000003',transaction_timestamp(),transaction_timestamp());
UPDATE spyglass.integration_connections SET state='active',credential_id='82900000-0000-4000-8000-000000000009',credential_generation=1,version=2,updated_at=transaction_timestamp()
WHERE account_id='82100000-0000-4000-8000-000000000001' AND id='82700000-0000-4000-8000-000000000007';
INSERT INTO spyglass.integration_executions
  (account_id,id,release_id,release_version,approval_id,capability,connection_id,connection_revision_id,connection_revision,credential_id,credential_generation,payload_sha256,state,created_at,updated_at)
VALUES ('82100000-0000-4000-8000-000000000001','82a00000-0000-4000-8000-00000000000a','82600000-0000-4000-8000-000000000006',3,'82400000-0000-4000-8000-000000000004','email.send',
  '82700000-0000-4000-8000-000000000007','82800000-0000-4000-8000-000000000008',1,'82900000-0000-4000-8000-000000000009',1,
  decode('436a8a0da3d689175e2f40e75400b8ded51045764813bfb0f094bcc9e6da7b31','hex'),'prepared',transaction_timestamp(),transaction_timestamp());

COMMIT;
