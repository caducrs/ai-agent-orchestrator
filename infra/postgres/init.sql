CREATE USER orchestrator WITH PASSWORD 'orchestrator';
CREATE DATABASE orchestrator OWNER orchestrator;
CREATE USER llm WITH PASSWORD 'llm';
CREATE DATABASE llm_gateway OWNER llm;
CREATE USER code_agent WITH PASSWORD 'code_agent';
CREATE DATABASE code_agent OWNER code_agent;
CREATE USER log_agent WITH PASSWORD 'log_agent';
CREATE DATABASE log_agent OWNER log_agent;
CREATE USER database_agent WITH PASSWORD 'database_agent';
CREATE DATABASE database_agent OWNER database_agent;
CREATE USER infrastructure_agent WITH PASSWORD 'infrastructure_agent';
CREATE DATABASE infrastructure_agent OWNER infrastructure_agent;
CREATE USER demo_owner WITH PASSWORD 'demo_owner';
CREATE DATABASE target_demo OWNER demo_owner;
CREATE USER diagnostic_reader WITH PASSWORD 'diagnostic_reader';
ALTER ROLE diagnostic_reader SET default_transaction_read_only = on;

\connect target_demo
CREATE TABLE incident_events (
    id bigserial PRIMARY KEY,
    service text NOT NULL,
    status integer NOT NULL,
    message text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO incident_events(service,status,message) VALUES
('demo-app',500,'database dependency timeout'),
('demo-app',500,'nil dependency while handling request'),
('demo-app',200,'health check successful');
GRANT CONNECT ON DATABASE target_demo TO diagnostic_reader;
GRANT USAGE ON SCHEMA public TO diagnostic_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO diagnostic_reader;
ALTER DEFAULT PRIVILEGES FOR ROLE demo_owner IN SCHEMA public GRANT SELECT ON TABLES TO diagnostic_reader;
