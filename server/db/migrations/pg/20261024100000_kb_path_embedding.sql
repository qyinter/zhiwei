-- 知识库目录 + 向量检索。
--
-- path：文档所在目录，用「/」分隔的多级路径（如「计费/退款」），空串 = 根目录。
-- embedding：块的向量（REAL[]，不依赖 pgvector），由后台向量化任务异步回填；
-- embed_model 记录生成它的模型，换模型后不一致的行会被重新向量化。
ALTER TABLE kb_documents ADD COLUMN path TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_kb_documents_path ON kb_documents (path, title);

ALTER TABLE kb_chunks ADD COLUMN embedding REAL[];
ALTER TABLE kb_chunks ADD COLUMN embed_model TEXT NOT NULL DEFAULT '';

ALTER TABLE kb_reflections ADD COLUMN embedding REAL[];
ALTER TABLE kb_reflections ADD COLUMN embed_model TEXT NOT NULL DEFAULT '';
