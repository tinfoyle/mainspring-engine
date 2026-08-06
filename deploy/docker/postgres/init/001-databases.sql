CREATE ROLE tenant_demo LOGIN PASSWORD 'tenant_demo';
CREATE ROLE tenant_saas LOGIN PASSWORD 'tenant_saas';
CREATE DATABASE mainspring_control OWNER mainspring;
CREATE DATABASE tenant_demo OWNER tenant_demo;
CREATE DATABASE tenant_saas OWNER tenant_saas;

\connect tenant_demo
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS vector;

\connect tenant_saas
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS vector;
