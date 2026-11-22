-- Short paraphrases, not a replacement for the official descriptors.
-- Verified against the official C1 Advanced handbook on 2026-11-21.
-- The application owns PRAGMA user_version; this additive seed is safe to rerun.
CREATE TABLE IF NOT EXISTS assessment_rubric_steps (
 assessment TEXT NOT NULL CHECK(length(trim(assessment)) > 0),
 step_key TEXT NOT NULL CHECK(length(trim(step_key)) > 0),
 step_order INTEGER NOT NULL CHECK(step_order > 0),
 title TEXT NOT NULL CHECK(length(trim(title)) > 0),
 summary TEXT NOT NULL CHECK(length(trim(summary)) > 0),
 score_guidance TEXT NOT NULL CHECK(length(trim(score_guidance)) > 0),
 source_url TEXT NOT NULL CHECK(length(trim(source_url)) > 0),
 source_pages TEXT NOT NULL CHECK(length(trim(source_pages)) > 0),
 source_sha256 TEXT NOT NULL CHECK(length(source_sha256) = 64 AND source_sha256 NOT GLOB '*[^0-9a-f]*'),
 reference_version TEXT NOT NULL CHECK(length(trim(reference_version)) > 0),
 PRIMARY KEY(assessment,step_key),
 UNIQUE(assessment,step_order)
);

INSERT INTO assessment_rubric_steps
 (assessment,step_key,step_order,title,summary,score_guidance,source_url,source_pages,source_sha256,reference_version)
VALUES ('cae','content',1,'Content','Assess assignment coverage and how completely the reader is informed.','5: Complete, relevant coverage.
4: Mix of 3/5.
3: Minor omissions/digressions; reader mostly informed.
2: Mix of 1/3.
1: Misunderstanding/irrelevance; reader barely informed.
0: Irrelevant; reader uninformed.','https://www.cambridgeenglish.org/Images/167804-c1-advanced-handbook.pdf','33, 35 (PDF pages 35, 37)','de53d5853405f3e25163c90dcd3771c83c9df4a87ce5d9a7163b785964f9ccb5','cambridge-c1-writing-2026-11-21-v1')
ON CONFLICT(assessment,step_key) DO NOTHING;

INSERT INTO assessment_rubric_steps
 (assessment,step_key,step_order,title,summary,score_guidance,source_url,source_pages,source_sha256,reference_version)
VALUES ('cae','communicative_achievement',2,'Communicative Achievement','Assess genre, register, purpose and reader engagement.','5: Flexible conventions communicate complex ideas effectively and fulfil purposes.
4: Mix of 3/5.
3: Effective conventions convey simple/complex ideas.
2: Mix of 1/3.
1: Conventions engage readers through straightforward ideas.
0: Below 1.','https://www.cambridgeenglish.org/Images/167804-c1-advanced-handbook.pdf','33, 35 (PDF pages 35, 37)','de53d5853405f3e25163c90dcd3771c83c9df4a87ce5d9a7163b785964f9ccb5','cambridge-c1-writing-2026-11-21-v1')
ON CONFLICT(assessment,step_key) DO NOTHING;

INSERT INTO assessment_rubric_steps
 (assessment,step_key,step_order,title,summary,score_guidance,source_url,source_pages,source_sha256,reference_version)
VALUES ('cae','organisation',3,'Organisation','Assess coherence, paragraph structure, progression and connections.','5: Unified text; flexible structure and connections.
4: Mix of 3/5.
3: Coherent structure; varied connections generally effective.
2: Mix of 1/3.
1: Mostly coherent; varied linking.
0: Below 1.','https://www.cambridgeenglish.org/Images/167804-c1-advanced-handbook.pdf','33, 35 (PDF pages 35, 37)','de53d5853405f3e25163c90dcd3771c83c9df4a87ce5d9a7163b785964f9ccb5','cambridge-c1-writing-2026-11-21-v1')
ON CONFLICT(assessment,step_key) DO NOTHING;

INSERT INTO assessment_rubric_steps
 (assessment,step_key,step_order,title,summary,score_guidance,source_url,source_pages,source_sha256,reference_version)
VALUES ('cae','language',4,'Language','Assess vocabulary and grammar for range, accuracy and control.','5: Wide, precise vocabulary; varied grammar with flexible control; errors limited to slips/less-common forms.
4: Mix of 3/5.
3: Varied, sometimes advanced vocabulary; flexible simple/complex grammar; occasional non-obstructive errors.
2: Mix of 1/3.
1: Everyday vocabulary; some complex grammar controlled; non-obstructive errors.
0: Below 1.','https://www.cambridgeenglish.org/Images/167804-c1-advanced-handbook.pdf','33, 35 (PDF pages 35, 37)','de53d5853405f3e25163c90dcd3771c83c9df4a87ce5d9a7163b785964f9ccb5','cambridge-c1-writing-2026-11-21-v1')
ON CONFLICT(assessment,step_key) DO NOTHING;
