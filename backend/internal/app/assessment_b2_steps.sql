-- B2 First for Schools descriptors transcribed from the user-supplied image.
-- Shared rows for Bands 4, 2 and 0 are repeated for independent step retrieval.
-- Keep this rubric separate from C1 Advanced. Existing saved edits are preserved.
INSERT INTO assessment_rubric_steps
 (assessment,step_key,step_order,title,summary,score_guidance,source_url,source_pages,source_sha256,reference_version)
VALUES ('b2_first_schools','content',1,'Content','Assess relevance to the task and how fully the target reader is informed.','5: All content is relevant to the task. Target reader is fully informed.
4: Performance shares features of Bands 3 and 5.
3: Minor irrelevances and/or omissions may be present. Target reader is on the whole informed.
2: Performance shares features of Bands 1 and 3.
1: Irrelevances and misinterpretation of task may be present. Target reader is minimally informed.
0: Content is totally irrelevant. Target reader is not informed.','attachment:sha256:d7248c5031c59695c5d5a7ff7a5555ab0fa8f1833fc3e4cfd33bff626cb68139','User-supplied image: right-hand B2 First for Schools rubric; no printed page number visible','d7248c5031c59695c5d5a7ff7a5555ab0fa8f1833fc3e4cfd33bff626cb68139','cambridge-b2-first-schools-2026-11-22-v1')
ON CONFLICT(assessment,step_key) DO NOTHING;

INSERT INTO assessment_rubric_steps
 (assessment,step_key,step_order,title,summary,score_guidance,source_url,source_pages,source_sha256,reference_version)
VALUES ('b2_first_schools','communicative_achievement',2,'Communicative Achievement','Assess use of task conventions, reader engagement and communication of ideas.','5: Uses the conventions of the communicative task effectively to hold the target reader’s attention and communicate straightforward and complex ideas, as appropriate.
4: Performance shares features of Bands 3 and 5.
3: Uses the conventions of the communicative task to hold the target reader’s attention and communicate straightforward ideas.
2: Performance shares features of Bands 1 and 3.
1: Uses the conventions of the communicative task in generally appropriate ways to communicate straightforward ideas.
0: Performance below Band 1.','attachment:sha256:d7248c5031c59695c5d5a7ff7a5555ab0fa8f1833fc3e4cfd33bff626cb68139','User-supplied image: right-hand B2 First for Schools rubric; no printed page number visible','d7248c5031c59695c5d5a7ff7a5555ab0fa8f1833fc3e4cfd33bff626cb68139','cambridge-b2-first-schools-2026-11-22-v1')
ON CONFLICT(assessment,step_key) DO NOTHING;

INSERT INTO assessment_rubric_steps
 (assessment,step_key,step_order,title,summary,score_guidance,source_url,source_pages,source_sha256,reference_version)
VALUES ('b2_first_schools','organisation',3,'Organisation','Assess coherence and use of linking words, cohesive devices and organisational patterns.','5: Text is well organised and coherent, using a variety of cohesive devices and organisational patterns to generally good effect.
4: Performance shares features of Bands 3 and 5.
3: Text is generally well organised and coherent, using a variety of linking words and cohesive devices.
2: Performance shares features of Bands 1 and 3.
1: Text is connected and coherent, using basic linking words and a limited number of cohesive devices.
0: Performance below Band 1.','attachment:sha256:d7248c5031c59695c5d5a7ff7a5555ab0fa8f1833fc3e4cfd33bff626cb68139','User-supplied image: right-hand B2 First for Schools rubric; no printed page number visible','d7248c5031c59695c5d5a7ff7a5555ab0fa8f1833fc3e4cfd33bff626cb68139','cambridge-b2-first-schools-2026-11-22-v1')
ON CONFLICT(assessment,step_key) DO NOTHING;

INSERT INTO assessment_rubric_steps
 (assessment,step_key,step_order,title,summary,score_guidance,source_url,source_pages,source_sha256,reference_version)
VALUES ('b2_first_schools','language',4,'Language','Assess vocabulary and grammatical range, control, and the effect of errors on communication.','5: Uses a range of vocabulary, including less common lexis, appropriately. Uses a range of simple and complex grammatical forms with control and flexibility. Occasional errors may be present but do not impede communication.
4: Performance shares features of Bands 3 and 5.
3: Uses a range of everyday vocabulary appropriately, with occasional inappropriate use of less common lexis. Uses a range of simple and some complex grammatical forms with a good degree of control. Errors do not impede communication.
2: Performance shares features of Bands 1 and 3.
1: Uses everyday vocabulary generally appropriately, while occasionally overusing certain lexis. Uses simple grammatical forms with a good degree of control. While errors are noticeable, meaning can still be determined.
0: Performance below Band 1.','attachment:sha256:d7248c5031c59695c5d5a7ff7a5555ab0fa8f1833fc3e4cfd33bff626cb68139','User-supplied image: right-hand B2 First for Schools rubric; no printed page number visible','d7248c5031c59695c5d5a7ff7a5555ab0fa8f1833fc3e4cfd33bff626cb68139','cambridge-b2-first-schools-2026-11-22-v1')
ON CONFLICT(assessment,step_key) DO NOTHING;
