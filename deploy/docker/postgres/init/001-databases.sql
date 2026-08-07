CREATE ROLE tenant_demo LOGIN PASSWORD 'tenant_demo';
CREATE DATABASE mainspring_control OWNER mainspring;
CREATE DATABASE tenant_demo OWNER tenant_demo;

\connect tenant_demo
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS vector;
